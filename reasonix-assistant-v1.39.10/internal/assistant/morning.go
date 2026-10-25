package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reasonix/internal/heartbeat"
	"time"
)

func (s MorningService) Snapshot(ctx context.Context, id string) (MorningSnapshot, error) {
	var raw string
	var v MorningSnapshot
	e := NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='morning_snapshot' AND id=?", id).Scan(&raw)
	})
	if e != nil {
		return v, e
	}
	return v, json.Unmarshal([]byte(raw), &v)
}
func (s MorningService) Prepare(ctx context.Context, t heartbeat.Task) (MorningSnapshot, error) {
	if v, e := s.Snapshot(ctx, t.ID); e == nil {
		return v, nil
	} else if !errors.Is(e, sql.ErrNoRows) {
		return v, e
	}
	now := s.now().In(mustShanghai())
	v := MorningSnapshot{ID: t.ID, Date: now.Format("2006-01-02"), CreatedAt: now, MailIDs: []string{}, Mail: []MailMessage{}, Todos: []Todo{}, MailStatus: "unavailable", CourseStatus: "unavailable"}
	hs := HeartbeatService{Home: s.Home}
	cfg, e := hs.Config(ctx)
	if e != nil {
		return v, e
	}
	if ok, e := hs.Allows(ctx, t, now); e != nil {
		return v, e
	} else if !ok {
		return v, heartbeat.ErrDeferred
	}
	mail := NewMailService(s.Home)
	if s.Mail != nil {
		mail = *s.Mail
	}
	if cfg.ModuleEnabled["mail_check"] {
		pollCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
		_, e = mail.Poll(pollCtx, Options{Workspace: t.WorkspaceRoot, Model: t.Model, Target: t.Target, MorningJobID: t.ID, MorningAttempt: t.RunStartedAt, MailScheduleID: t.ID, TriggerID: t.TriggerID})
		cancel()
		if e == nil {
			v.MailStatus = "ok"
		}
	} else {
		v.MailStatus = "paused"
	}
	newsCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	v.News = s.prepareHotNews(newsCtx)
	cancel()
	_, e = NewWeatherService(s.Home).Config()
	v.WeatherConfigured = e == nil
	err := NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		job, e := loadMorningJob(ctx, tx, t.ID)
		if e != nil {
			return e
		}
		v.MailIDs = job.MailIDs
		if v.MailIDs == nil {
			v.MailIDs = []string{}
		}
		mails, remaining, status, e := morningMailState(ctx, tx, job)
		if e != nil {
			return e
		}
		v.Mail = mails
		v.RemainingMail = remaining
		if v.MailStatus == "ok" {
			v.MailStatus = status
		}
		if e := loadMorningFacts(ctx, tx, &v, cfg, now); e != nil {
			return e
		}
		return saveEntity(ctx, tx, "morning_snapshot", t.ID, v)
	})
	return v, err
}
func morningTodos(all []Todo, now time.Time, planned map[string]bool) []Todo {
	out := []Todo{}
	date := now.In(mustShanghai()).Format("2006-01-02")
	for _, v := range all {
		if v.Status != "open" {
			continue
		}
		due, e := time.Parse(time.RFC3339, v.DueAt)
		if v.PlannedFor == date || planned[v.JobID] || e == nil && due.In(mustShanghai()).Format("2006-01-02") <= date {
			out = append(out, v)
		}
	}
	return out
}
