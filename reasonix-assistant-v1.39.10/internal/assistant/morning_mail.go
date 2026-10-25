package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reasonix/internal/heartbeat"
)

func loadMorningJob(ctx context.Context, tx *sql.Tx, id string) (heartbeat.Task, error) {
	var raw string
	var t heartbeat.Task
	e := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='job' AND id=?", id).Scan(&raw)
	if e != nil {
		return t, e
	}
	return t, json.Unmarshal([]byte(raw), &t)
}
func bindMorningMail(ctx context.Context, tx *sql.Tx, records map[string]mailRecord, o Options, ids []string, selected map[string]bool) (int, error) {
	t, e := loadMorningJob(ctx, tx, o.MorningJobID)
	if e != nil {
		return 0, e
	}
	if t.ModuleID != "morning_briefing" || len(t.History) == 0 || t.History[len(t.History)-1].StartedAt != o.MorningAttempt || t.History[len(t.History)-1].Status != "running" {
		return 0, errors.New("invalid morning mail owner")
	}
	if len(t.MailIDs) > 0 {
		return 0, nil
	}
	t.MailIDs = ids
	if e = saveEntity(ctx, tx, "job", t.ID, t); e != nil {
		return 0, e
	}
	for key, r := range records {
		if selected[r.Dedup] && r.JobID == "" {
			r.JobID = t.ID
			records[key] = r
		}
	}
	return len(ids), nil
}
func morningMailState(ctx context.Context, tx *sql.Tx, t heartbeat.Task) ([]MailMessage, int, string, error) {
	records, e := loadEntities[mailRecord](ctx, tx, "mail_message")
	if e != nil {
		return nil, 0, "unavailable", e
	}
	byID := map[string]MailMessage{}
	remaining := 0
	seen := map[string]bool{}
	for _, r := range records {
		byID[r.Message.ID] = r.Message
		if r.Observed && r.JobID == "" && !seen[r.Dedup] {
			remaining++
			seen[r.Dedup] = true
		}
	}
	mails := []MailMessage{}
	for _, id := range t.MailIDs {
		m, ok := byID[id]
		if !ok {
			return nil, 0, "unavailable", errors.New("reserved mail disappeared")
		}
		mails = append(mails, m)
	}
	health, e := loadEntities[MailHealth](ctx, tx, "mail_health")
	if e != nil {
		return nil, 0, "unavailable", e
	}
	status := "ok"
	failures := 0
	for _, h := range health {
		if h.Error != "" {
			failures++
		}
	}
	if failures > 0 {
		status = "partial"
	}
	if len(health) > 0 && failures == len(health) {
		status = "unavailable"
	}
	return mails, remaining, status, nil
}
