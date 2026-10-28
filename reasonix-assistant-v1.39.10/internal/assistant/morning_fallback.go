package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"reasonix/internal/heartbeat"
)

// Fallback never calls a provider, invents mail decisions, or bypasses admission.
// Its result uses the normal delivery/acknowledgement path and stable job ID.
func (s MorningService) Fallback(ctx context.Context, t heartbeat.Task, raw string) (string, error) {
	now := s.now().In(mustShanghai())
	allowed, err := (HeartbeatService{Home: s.Home}).Allows(ctx, t, now)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", heartbeat.ErrDeferred
	}
	snap, err := s.Snapshot(ctx, t.ID)
	if errors.Is(err, sql.ErrNoRows) {
		snap = MorningSnapshot{ID: t.ID, Date: now.Format("2006-01-02"), CreatedAt: now}
	} else if err != nil {
		return "", err
	}
	if snap.Date != now.Format("2006-01-02") {
		return "", errors.New("morning fallback snapshot has wrong civil date")
	}
	if err = s.refreshFacts(ctx, &snap); err != nil {
		return "", err
	}
	var draft MorningOutput
	parsed := json.Unmarshal([]byte(raw), &draft) == nil
	out := MorningOutput{Weather: "今天的天气还没整理好", MailDecisions: []MailDecision{}, News: []MorningNews{}}
	var weatherRef map[string]any
	if parsed && validateMorningWeather(draft.Weather) == nil && snap.WeatherConfigured && NewWeatherService(s.Home).VerifyWeatherRead(ctx, t.ID, t.RunStartedAt) == nil {
		if ref, e := s.weatherReference(ctx, t); e == nil {
			out.Weather, weatherRef = draft.Weather, ref
		}
	}
	if parsed && validateMorningNews(snap, draft.News) == nil {
		out.News = draft.News
	} else {
		snap.News.Items = nil
		snap.News.Status = "unavailable"
	}
	// Validate each decision against its own host-bound ID and read receipt.
	// Duplicate or missing IDs are requeued rather than guessed.
	mailPaused := snap.MailStatus == "paused"
	mailValid := false
	invalid := map[string]bool{}
	for _, id := range snap.MailIDs {
		invalid[id] = true
	}
	if parsed {
		filtered, e := NewMailService(s.Home).filterDeliveredDecisions(ctx, draft.MailDecisions)
		if e != nil {
			return "", e
		}
		counts := map[string]int{}
		for _, d := range filtered {
			counts[d.ID]++
		}
		for _, d := range filtered {
			if !invalid[d.ID] || counts[d.ID] != 1 {
				continue
			}
			oneSnap := snap
			oneSnap.MailIDs = []string{d.ID}
			oneTask := t
			oneTask.MailIDs = []string{d.ID}
			if s.verifyMail(ctx, oneTask, oneSnap, []MailDecision{d}) != nil {
				continue
			}
			delete(invalid, d.ID)
			out.MailDecisions = append(out.MailDecisions, d)
		}
		if len(out.MailDecisions) > 0 || (len(snap.MailIDs) == 0 && draft.MailDecisions != nil) {
			subtask := t
			subtask.MailIDs = nil
			anyNotify := false
			for _, d := range out.MailDecisions {
				subtask.MailIDs = append(subtask.MailIDs, d.ID)
				anyNotify = anyNotify || *d.Notify
			}
			decisions, e := json.Marshal(map[string]any{"notify": anyNotify, "mail_decisions": out.MailDecisions})
			if e != nil {
				return "", e
			}
			if e = NewMailService(s.Home).RecordMailDecisions(ctx, subtask, string(decisions)); e != nil {
				return "", e
			}
			mailValid = true
		}
	}
	if mailValid && len(invalid) > 0 {
		snap.RemainingMail += len(invalid)
	}

	if !mailValid {
		snap.MailStatus = "unavailable"
	}
	lines := strings.Split(renderMorning(snap, out), "\n")
	if weatherRef == nil {
		lines[1] = "今天的天气还没整理好。"
	}
	if !mailValid {
		lines[3] = "邮件还没整理好，暂时没有可靠的摘要。"
		if mailPaused {
			snap.MailStatus = "paused"
			lines[3] = "邮件自动检查还暂停着。"
		}
	} else {
		questions, e := (Proposals{Home: s.Home}).questionsForMail(ctx, t, out.MailDecisions, 100)
		if e != nil {
			return "", e
		}
		if len(questions) > 0 {
			lines[3] = strings.TrimSuffix(lines[3], "。") + "；" + questions[0]
			if len(questions) > 1 {
				lines[3] += fmt.Sprintf("另外还有%d项邮件待办建议，您可以让我列出来。", len(questions)-1)
			}
		}
	}
	if len(out.News) == 0 && snap.News.Status == "unavailable" {
		lines[5] = "AI新闻还没整理好，稍后可以再看。"
	}
	message := strings.Join(lines, "\n")
	err = NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		if !mailValid {
			if e := releaseFallbackMail(ctx, tx, t.ID); e != nil {
				return e
			}
		} else if len(invalid) > 0 {
			if e := releaseFallbackMailIDs(ctx, tx, t.ID, invalid); e != nil {
				return e
			}
		}
		displayed := []string{}
		for _, d := range out.MailDecisions {
			if *d.Notify {
				displayed = append(displayed, d.ID)
			}
		}
		return saveEntity(ctx, tx, "morning_result", courseReadID(t.ID, t.RunStartedAt), map[string]any{
			"snapshot": snap, "output": out, "message": message, "fallback": true,
			"displayed_mail_ids": displayed, "weather_reference": weatherRef,
		})
	})
	return message, err
}

func releaseFallbackMail(ctx context.Context, tx *sql.Tx, job string) error {
	records, err := loadEntities[mailRecord](ctx, tx, "mail_message")
	if err != nil {
		return err
	}
	for _, r := range records {
		if r.JobID == job {
			r.JobID = ""
			if err = saveEntity(ctx, tx, "mail_message", r.Message.ID, r); err != nil {
				return err
			}
		}
	}
	// Finalize may have saved tentative decisions before a later storage error.
	// They must not become successful silent mail or unasked proposals when this
	// fallback is delivered. Actual user-confirmed tasks are never touched.
	_, err = tx.ExecContext(ctx, "DELETE FROM assistant_entities WHERE (kind='mail_decision' AND json_extract(payload,'$.job_id')=?) OR (kind='task_proposal' AND json_extract(payload,'$.analysis_job_id')=? AND json_extract(payload,'$.state')='pending')", job, job)
	return err
}

func releaseFallbackMailIDs(ctx context.Context, tx *sql.Tx, job string, ids map[string]bool) error {
	records, err := loadEntities[mailRecord](ctx, tx, "mail_message")
	if err != nil {
		return err
	}
	for _, r := range records {
		if r.JobID == job && ids[r.Message.ID] {
			r.JobID = ""
			if err = saveEntity(ctx, tx, "mail_message", r.Message.ID, r); err != nil {
				return err
			}
		}
	}
	for id := range ids {
		if _, err = tx.ExecContext(ctx, "DELETE FROM assistant_entities WHERE kind='mail_decision' AND id=? AND json_extract(payload,'$.job_id')=?", id, job); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM assistant_entities WHERE kind='task_proposal' AND json_extract(payload,'$.mail_id')=? AND json_extract(payload,'$.analysis_job_id')=? AND json_extract(payload,'$.state')='pending'", id, job); err != nil {
			return err
		}
	}
	return nil
}
