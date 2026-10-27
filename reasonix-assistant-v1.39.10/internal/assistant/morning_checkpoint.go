package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"reasonix/internal/heartbeat"
)

// A checked draft is recovery data, not a mail decision, proposal or delivery.
// It is scoped to one host-owned run and must pass Finalize again before use.
type morningCheckpoint struct {
	JobID   string        `json:"job_id"`
	Attempt int64         `json:"attempt"`
	Date    string        `json:"date"`
	Output  MorningOutput `json:"output"`
}

func currentMorningAttempt(ctx context.Context, tx *sql.Tx, t heartbeat.Task) (heartbeat.Task, error) {
	job, err := loadMorningJob(ctx, tx, t.ID)
	if err != nil {
		return job, err
	}
	if !job.Enabled || job.ModuleID != "morning_briefing" || t.RunStartedAt <= 0 || len(job.History) == 0 {
		return job, heartbeat.ErrDeferred
	}
	last := job.History[len(job.History)-1]
	if last.Status != "running" || last.StartedAt != t.RunStartedAt {
		return job, heartbeat.ErrDeferred
	}
	job.RunStartedAt = t.RunStartedAt
	return job, nil
}

func (s MorningService) checkpointTask(ctx context.Context, t heartbeat.Task) (heartbeat.Task, error) {
	var job heartbeat.Task
	err := NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		var err error
		job, err = currentMorningAttempt(ctx, tx, t)
		return err
	})
	if err != nil {
		return job, err
	}
	allowed, err := (HeartbeatService{Home: s.Home}).Allows(ctx, job, s.now())
	if err == nil && !allowed {
		err = heartbeat.ErrDeferred
	}
	return job, err
}

func (s MorningService) saveCheckedDraft(ctx context.Context, t heartbeat.Task, snap MorningSnapshot, out MorningOutput) error {
	if _, err := s.checkpointTask(ctx, t); err != nil {
		return err
	}
	if snap.ID != t.ID || snap.Date != s.now().In(mustShanghai()).Format("2006-01-02") {
		return errors.New("checked morning draft has wrong identity or civil date")
	}
	return NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		if _, err := currentMorningAttempt(ctx, tx, t); err != nil {
			return err
		}
		return saveEntity(ctx, tx, "morning_checked_draft", courseReadID(t.ID, t.RunStartedAt), morningCheckpoint{
			JobID: t.ID, Attempt: t.RunStartedAt, Date: snap.Date, Output: out,
		})
	})
}

// RecoverCheckedDraft never invokes the model or acknowledges delivery. Current
// admission, reads, source identities and candidate evidence are revalidated.
func (s MorningService) RecoverCheckedDraft(ctx context.Context, t heartbeat.Task) (string, error) {
	job, err := s.checkpointTask(ctx, t)
	if err != nil {
		return "", err
	}
	var draft morningCheckpoint
	err = NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		var raw string
		if err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='morning_checked_draft' AND id=?", courseReadID(job.ID, job.RunStartedAt)).Scan(&raw); err != nil {
			return err
		}
		return json.Unmarshal([]byte(raw), &draft)
	})
	if err != nil {
		return "", err
	}
	if draft.JobID != job.ID || draft.Attempt != job.RunStartedAt || draft.Date != s.now().In(mustShanghai()).Format("2006-01-02") {
		return "", errors.New("checked morning draft has wrong identity or civil date")
	}
	snap, err := s.Snapshot(ctx, job.ID)
	if err != nil {
		return "", err
	}
	if snap.ID != job.ID || snap.Date != draft.Date {
		return "", errors.New("checked morning source snapshot has wrong identity or civil date")
	}
	raw, err := json.Marshal(draft.Output)
	if err != nil {
		return "", err
	}
	return s.Finalize(ctx, job, string(raw))
}
