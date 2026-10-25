package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reasonix/internal/heartbeat"
	"strings"
	"unicode/utf8"
)

func (s HotNewsService) executionSnapshot(ctx context.Context, t heartbeat.Task) (HotNewsSnapshot, error) {
	var b newsExecutionBinding
	var raw string
	e := NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='ai_news_binding' AND id=?", courseReadID(t.ID, t.RunStartedAt)).Scan(&raw)
	})
	if e != nil {
		return HotNewsSnapshot{}, e
	}
	if e = json.Unmarshal([]byte(raw), &b); e != nil {
		return HotNewsSnapshot{}, e
	}
	return s.loadSnapshot(ctx, b.SnapshotID)
}
func (s HotNewsService) Finalize(ctx context.Context, t heartbeat.Task, raw string) (string, error) {
	var output NewsBriefingOutput
	if e := json.Unmarshal([]byte(raw), &output); e != nil {
		return "", e
	}
	snap, e := s.executionSnapshot(ctx, t)
	if e != nil {
		return "", e
	}
	if e = s.verifyNewsOutput(ctx, t, snap, output); e != nil {
		return "", e
	}
	text := renderNewsBriefing(snap, output)
	result := map[string]any{"snapshot": snap, "output": output, "message": text}
	e = NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		return saveEntity(ctx, tx, "ai_news_result", courseReadID(t.ID, t.RunStartedAt), result)
	})
	return text, e
}
func (s HotNewsService) verifyNewsOutput(ctx context.Context, t heartbeat.Task, snap HotNewsSnapshot, v NewsBriefingOutput) error {
	if v.Events == nil || len(v.Events) > 7 || utf8.RuneCountInString(v.Note) > 400 {
		return errors.New("news requires at most seven structured events")
	}
	if len(v.Events) < min(7, len(snap.Candidates)) && strings.TrimSpace(v.Note) == "" {
		return errors.New("fewer than seven events requires a reason")
	}
	positions := map[string]int{}
	for i, a := range snap.Candidates {
		positions[a.ID] = i
	}
	return NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		key := courseReadID(t.ID, t.RunStartedAt)
		var proof string
		if e := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='ai_news_list_receipt' AND id=?", key).Scan(&proof); e != nil || proof != "true" {
			return errors.New("news must actually list the bound snapshot")
		}
		seen := map[string]bool{}
		last := -1
		for _, event := range v.Events {
			if len(event.IDs) == 0 || len(event.IDs) > 7 || strings.TrimSpace(event.Title) == "" || strings.TrimSpace(event.Summary) == "" || utf8.RuneCountInString(event.Title)+utf8.RuneCountInString(event.Summary) > 50 {
				return errors.New("news event requires title and summary totaling at most 50 characters including punctuation")
			}
			first := len(snap.Candidates)
			for _, id := range event.IDs {
				at, ok := positions[id]
				if !ok || seen[id] {
					return errors.New("news used an unknown or repeated ID")
				}
				seen[id] = true
				first = min(first, at)
				if e := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='ai_news_read_receipt' AND id=?", key+":"+id).Scan(&proof); e != nil || proof != "true" {
					return errors.New("news must read selected source content")
				}
				if snap.Candidates[at].ContentStatus == "insufficient_content" || snap.Candidates[at].Text == "" {
					return errors.New("news has no usable content for selected event")
				}
			}
			if first <= last {
				return errors.New("news event order must preserve host ranking")
			}
			last = first
		}
		if len(v.Events) == 0 && len(snap.Candidates) > 0 {
			return errors.New("cannot declare no news when candidates exist")
		}
		return nil
	})
}
func renderNewsBriefing(snap HotNewsSnapshot, v NewsBriefingOutput) string {
	parts := []string{"# **AI News**", fmt.Sprintf("Sir，近24小时精选的%d个AI热点事件，下面逐条说。", len(v.Events))}
	if len(v.Events) == 0 {
		parts[1] = "Sir，过去24小时暂时没有找到可确认的AI新闻。"
		if snap.Status == "unavailable" {
			parts[1] = "Sir，新闻来源暂时没能读取，稍后再看看。"
		}
	}
	byID := map[string]HotNewsArticle{}
	for _, a := range snap.Candidates {
		byID[a.ID] = a
	}
	for i, event := range v.Events {
		a := byID[event.IDs[0]]
		when := "来源发布时间"
		if a.TimeBasis == "discussion_submission" {
			when = "HN讨论提交时间（原文日期未确认）"
		}
		source := fmt.Sprintf("来源：%s；%s：%s", hotSourceLabels(a.Sources), when, a.PublishedAt.In(mustShanghai()).Format("01月02日 15:04"))
		if a.PublishedAt.Before(snap.Since) {
			source += "（原文较早，近期受到关注）"
		}
		parts = append(parts, fmt.Sprintf("**%d. %s**\n%s\n%s\n[原文](%s)", i+1, singleLine(event.Title), strings.TrimSpace(event.Summary), source, a.URL))
	}
	return strings.Join(parts, "\n\n")
}
func (s HotNewsService) Acknowledge(ctx context.Context, t heartbeat.Task, r heartbeat.Run) error {
	if t.ModuleID != "ai_news_briefing" {
		return nil
	}
	return NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		var raw string
		var v any
		if e := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='ai_news_result' AND id=?", courseReadID(t.ID, r.StartedAt)).Scan(&raw); e != nil {
			return e
		}
		if e := json.Unmarshal([]byte(raw), &v); e != nil {
			return e
		}
		return saveEntity(ctx, tx, "ai_news_delivered", t.ID, v)
	})
}
