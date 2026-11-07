package assistant

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"reasonix/internal/provider"
)

// RecordCompletedNewsTurn indexes only a completed foreground report backed by
// this turn's successful read and check receipts. Candidate browsing and follow-up
// answers never replace the latest full report. Callers exclude failed/cancelled turns.
func (s HotNewsService) RecordCompletedNewsTurn(ctx context.Context, source, sessionID string, messages []provider.Message) error {
	if source != "web" && source != "weixin" {
		return nil
	}
	if len(messages) == 0 {
		return nil
	}
	final := messages[len(messages)-1]
	if final.Role != provider.RoleAssistant || final.LocalOnly || len(final.ToolCalls) > 0 || !strings.Contains(final.Content, "# **AI News**") {
		return nil
	}
	type request struct {
		Action     string             `json:"action"`
		SnapshotID string             `json:"snapshot_id"`
		Events     []NewsEventSummary `json:"events"`
	}
	calls := map[string]request{}
	reads := map[string]map[string]HotNewsArticle{}
	var checked []NewsEventSummary
	for _, m := range messages[:len(messages)-1] {
		if m.LocalOnly {
			continue
		}
		for _, call := range m.ToolCalls {
			args := call.Arguments
			name := call.Name
			if name == "use_capability" && call.ResolvedName == "assistant_ai_news" {
				var proxy struct {
					Arguments json.RawMessage `json:"arguments"`
				}
				if json.Unmarshal([]byte(args), &proxy) != nil {
					continue
				}
				args = string(proxy.Arguments)
				name = call.ResolvedName
			}
			if name != "assistant_ai_news" {
				continue
			}
			var q request
			if json.Unmarshal([]byte(args), &q) == nil {
				calls[call.ID] = q
			}
		}
		if m.ToolRunState != "" && m.ToolRunState != provider.ToolRunCompleted {
			continue
		}
		if m.Role != provider.RoleTool {
			continue
		}
		q, ok := calls[m.ToolCallID]
		if !ok {
			continue
		}
		delete(calls, m.ToolCallID)
		if q.Action == "read" {
			var result struct {
				SnapshotID string           `json:"snapshot_id"`
				Articles   []HotNewsArticle `json:"articles"`
			}
			if json.Unmarshal([]byte(m.Content), &result) != nil || result.SnapshotID == "" || result.SnapshotID != q.SnapshotID {
				continue
			}
			if reads[result.SnapshotID] == nil {
				reads[result.SnapshotID] = map[string]HotNewsArticle{}
			}
			for _, a := range result.Articles {
				if a.ID != "" && strings.TrimSpace(a.Text) != "" {
					reads[result.SnapshotID][a.ID] = a
				}
			}
		}
		if q.Action == "check" {
			checked = nil
			var result struct {
				Valid bool `json:"valid"`
			}
			if json.Unmarshal([]byte(m.Content), &result) == nil && result.Valid && len(q.Events) > 0 && len(q.Events) <= 7 {
				checked = q.Events
			}
		}
	}
	if len(checked) == 0 {
		return nil
	}
	// The visible answer must contain every checked item, including a source URL
	// actually returned by read. A valid draft followed by a partial answer is not a report.
	normalize := func(v string) string { return strings.Join(strings.Fields(strings.ReplaceAll(v, "**", "")), "") }
	visible := normalize(final.Content)
	for snapshotID, articles := range reads {
		valid := true
		seen := map[string]bool{}
		for _, e := range checked {
			if strings.TrimSpace(e.Title) == "" || strings.TrimSpace(e.Summary) == "" || len(e.IDs) == 0 || !strings.Contains(visible, normalize(e.Title)) || !strings.Contains(visible, normalize(e.Summary)) {
				valid = false
				break
			}
			cited := false
			for _, id := range e.IDs {
				a, ok := articles[id]
				if !ok || seen[id] {
					valid = false
					break
				}
				seen[id] = true
				if a.URL != "" && strings.Contains(final.Content, a.URL) || a.EvidenceURL != "" && strings.Contains(final.Content, a.EvidenceURL) {
					cited = true
				}
			}
			if !cited {
				valid = false
			}
			if !valid {
				break
			}
		}
		if !valid {
			continue
		}
		snap, err := s.loadSnapshot(ctx, snapshotID)
		if err != nil {
			return err
		}
		at := s.now()
		if final.CreatedAt > 0 {
			at = time.UnixMilli(final.CreatedAt)
		}
		digest := sha256.Sum256([]byte(sessionID + "\x00" + source + "\x00" + final.ID + "\x00" + final.Content))
		value := map[string]any{"available": true, "source": source, "generated_at": at.UTC().Format(time.RFC3339Nano), "snapshot": snap, "output": NewsBriefingOutput{Events: checked}, "message": final.Content}
		return NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
			return saveEntity(ctx, tx, "ai_news_report", hex.EncodeToString(digest[:]), value)
		})
	}
	return nil
}

// latestReport also projects the public news section of an acknowledged morning
// report. No replay, notification or mutation is needed to expose existing days.
func (s HotNewsService) latestReport(ctx context.Context) (string, error) {
	var latest string
	var latestAt time.Time
	err := NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT payload FROM assistant_entities WHERE kind IN ('ai_news_report','ai_news_delivered') ORDER BY COALESCE(julianday(json_extract(payload,'$.generated_at')),julianday(json_extract(payload,'$.snapshot.as_of'))) DESC,rowid DESC LIMIT 1`).Scan(&latest)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if latest != "" {
			var meta struct {
				GeneratedAt time.Time       `json:"generated_at"`
				Snapshot    HotNewsSnapshot `json:"snapshot"`
			}
			if err := json.Unmarshal([]byte(latest), &meta); err != nil {
				return err
			}
			latestAt = meta.GeneratedAt
			if latestAt.IsZero() {
				latestAt = meta.Snapshot.AsOf
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT payload FROM assistant_entities WHERE kind='morning_delivered' AND json_array_length(json_extract(payload,'$.output.news'))>0 ORDER BY COALESCE(julianday(json_extract(payload,'$.generated_at')),julianday(json_extract(payload,'$.snapshot.created_at'))) DESC,rowid DESC LIMIT 32`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			report, at, ok := morningNewsReport(raw)
			if !ok {
				continue
			}
			if latest == "" || at.After(latestAt) {
				body, err := json.Marshal(report)
				if err != nil {
					return err
				}
				latest = string(body)
				latestAt = at
			}
		}
		return rows.Err()
	})
	if err != nil {
		return "", err
	}
	if latest == "" {
		return `{"available":false}`, nil
	}
	return latest, nil
}

func morningNewsReport(raw string) (map[string]any, time.Time, bool) {
	var result struct {
		Snapshot    MorningSnapshot `json:"snapshot"`
		Output      MorningOutput   `json:"output"`
		GeneratedAt time.Time       `json:"generated_at"`
	}
	if json.Unmarshal([]byte(raw), &result) != nil || len(result.Output.News) == 0 || len(result.Output.News) > 3 {
		return nil, time.Time{}, false
	}
	at := result.GeneratedAt
	basis := "generated_at"
	if at.IsZero() {
		at = result.Snapshot.CreatedAt
		basis = "snapshot_created_at"
	}
	if at.IsZero() {
		return nil, at, false
	}
	page := result.Snapshot.News
	snap := HotNewsSnapshot{ID: page.SnapshotID, Since: page.Since, AsOf: page.Until, Status: page.Status, Ranking: page.Ranking, TargetCount: len(result.Output.News)}
	byID := map[string]NewsItem{}
	for _, item := range page.Items {
		byID[item.ID] = item
	}
	output := NewsBriefingOutput{}
	seen := map[string]bool{}
	for _, n := range result.Output.News {
		item, ok := byID[n.ID]
		if !ok || seen[n.ID] || strings.TrimSpace(n.Summary) == "" || item.URL == "" {
			return nil, at, false
		}
		seen[n.ID] = true
		snap.Candidates = append(snap.Candidates, HotNewsArticle{ID: item.ID, Title: item.Title, URL: item.URL, EvidenceURL: item.IssueURL, PublishedAt: item.PublishedAt, Sources: item.Sources, TimeBasis: item.TimeBasis})
		output.Events = append(output.Events, NewsEventSummary{IDs: []string{n.ID}, Title: n.Summary})
	}
	// Only news fields enter the response. Morning mail, tasks, courses and weather
	// deliberately stay in their own views and never leak through this projection.
	return map[string]any{"available": true, "source": "morning", "generated_at": at.UTC().Format(time.RFC3339Nano), "time_basis": basis, "snapshot": snap, "output": output, "message": renderNewsBriefing(snap, output)}, at, true
}
