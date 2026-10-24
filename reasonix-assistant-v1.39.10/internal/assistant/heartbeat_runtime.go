package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"

	"time"

	"reasonix/internal/heartbeat"
)

type ModuleOccurrence struct {
	ID          string    `json:"id"`
	TriggerID   string    `json:"trigger_id"`
	Date        string    `json:"date"`
	Revision    int       `json:"revision"`
	ScheduledAt time.Time `json:"scheduled_at"`
	Modules     []string  `json:"modules"`
	JobIDs      []string  `json:"job_ids"`
}

// Prepare is native clock work only: no provider requests and no model call.
// Stable daily IDs survive moving a wake time and deleting its date override.
func (s HeartbeatService) Prepare(ctx context.Context, now time.Time) error {
	return s.repo().transact(ctx, func(tx *sql.Tx) error {
		c, err := loadHeartbeatConfig(ctx, tx)
		if err != nil {
			return err
		}
		if err = c.Validate(); err != nil {
			return err
		}
		now = now.In(mustShanghai())
		date := now.Format("2006-01-02")
		changed := false
		for d := range c.Overrides {
			if d < date {
				delete(c.Overrides, d)
				changed = true
			}
		}
		if changed {
			c.Revision++
			if err = saveEntity(ctx, tx, "heartbeat_config", heartbeatConfigID, c); err != nil {
				return err
			}
		}
		for _, tr := range c.Triggers {
			if err := s.prepareModuleTrigger(ctx, tx, c, tr, date, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// Admission concerns scheduled module work only. User conversations and explicit
// standalone reminders are never blocked by the routine.
func (s HeartbeatService) Allows(ctx context.Context, t heartbeat.Task, now time.Time) (bool, error) {
	if t.ModuleID == "" && t.Capability != "mail" && t.Capability != "weather" {
		return true, nil
	}
	c, err := s.Config(ctx)
	if err != nil {
		return false, err
	}
	module := t.ModuleID
	if module == "mail_analysis" || (t.Capability == "mail" && module != "mail_digest") {
		module = "mail_check"
	}
	if module == "" && t.Capability == "weather" {
		module = "weather_briefing"
	}
	if !c.ModuleEnabled[module] || t.Expired(now) {
		return false, nil
	}
	date := now.In(mustShanghai()).Format("2006-01-02")
	start, end, ok := c.Bounds(date)
	if tr, exists := c.Triggers[t.TriggerID]; exists {
		start, end, ok = c.TriggerBounds(tr, date)
	}
	if t.ModuleID == "mail_analysis" && (!ok || now.Before(start) || !now.Before(end)) {
		start, end, ok = c.Bounds(date)
	}
	if !ok || now.Before(start) || !now.Before(end) {
		return false, nil
	}
	if t.ModuleID == "mail_analysis" {
		return true, nil
	}
	if t.OccurrenceID != "" {
		allowed, err := s.allowsOccurrence(ctx, t, module)
		if err != nil || !allowed {
			return false, err
		}
	}
	if t.TriggerID != "" {
		tr, exists := c.Triggers[t.TriggerID]
		if !exists || !tr.Enabled {
			return false, nil
		}
		// A disabled or changed pending selection cannot leak from an old ready list.
		// A run already claimed keeps its own module snapshot until completion.
		if len(t.History) == 0 && !slices.Contains(c.Selected(tr, date), module) {
			return false, nil
		}
	}
	return true, nil
}

// CheckRunNotification closes the authenticated control-API delivery path too.
func (s HeartbeatService) CheckRunNotification(ctx context.Context, runID string, now time.Time) error {
	i := strings.LastIndex(runID, ":")
	if i < 0 {
		return nil
	}
	id := runID[:i]
	var job heartbeat.Task
	err := s.repo().transact(ctx, func(tx *sql.Tx) error {
		var raw string
		if e := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='job' AND id=?", id).Scan(&raw); e != nil {
			return e
		}
		return json.Unmarshal([]byte(raw), &job)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if job.ModuleID == "" && job.Capability != "mail" && job.Capability != "weather" {
		return nil
	}
	allowed, err := s.Allows(ctx, job, now)
	if err != nil {
		return err
	}
	if !allowed {
		return heartbeat.ErrDeferred
	}
	return nil
}

func (s HeartbeatService) Runs(ctx context.Context, r HeartbeatRequest) (any, error) {
	jobs, err := (JobStore{Repository: s.repo()}).List(ctx)
	if err != nil {
		return nil, err
	}
	type runView struct {
		Module  string `json:"module"`
		Trigger string `json:"trigger"`
		JobID   string `json:"job_id"`
		heartbeat.Run
	}
	records := []runView{}
	for _, j := range jobs {
		if j.ModuleID == "" && j.Capability != "mail" && j.Capability != "weather" {
			continue
		}
		if r.TriggerID != "" && j.TriggerID != r.TriggerID {
			continue
		}
		module := j.ModuleID
		if module == "" {
			module = j.Capability + "_analysis"
		}
		if r.ModuleID != "" && r.ModuleID != module && !(r.ModuleID == "mail_check" && module == "mail_analysis") {
			continue
		}
		for _, run := range j.History {
			if r.Date != "" && time.UnixMilli(run.StartedAt).In(mustShanghai()).Format("2006-01-02") != r.Date {
				continue
			}
			run.SessionID = ""
			run.ConversationID = ""
			if len([]rune(run.Result)) > 600 {
				run.Result = string([]rune(run.Result)[:600]) + " [truncated]"
			}
			records = append(records, runView{module, j.TriggerID, j.ID, run})
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].StartedAt > records[j].StartedAt })
	if len(records) > 30 {
		records = records[:30]
	}
	return map[string]any{"runs": records, "coverage": "Latest 30 available run records. completed with empty result and a decision reason means no notification was requested; delivery status is separate. Older per-job history may be truncated."}, nil
}
