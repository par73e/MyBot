package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reasonix/internal/heartbeat"
	"slices"
	"time"
)

func (s HeartbeatService) prepareModuleTrigger(ctx context.Context, tx *sql.Tx, c HeartbeatConfig, tr ModuleTrigger, date string, now time.Time) error {
	start, end, ok := c.TriggerBounds(tr, date)
	if !ok || now.Before(start) || !now.Before(end) || !tr.Enabled {
		return nil
	}
	at, key, ok := moduleTriggerTime(tr, date, start, now)
	if !ok {
		return nil
	}
	id := "hb-" + mailHash(key)
	skip, err := reconcileModuleOccurrence(ctx, tx, id, c.Revision, tr, at, now)
	if err != nil || skip {
		return err
	}
	enabled := []string{}
	for _, module := range c.Selected(tr, date) {
		if c.ModuleEnabled[module] {
			enabled = append(enabled, module)
		}
	}
	record := ModuleOccurrence{ID: id, TriggerID: tr.ID, Date: date, Revision: c.Revision, ScheduledAt: at, Modules: enabled, JobIDs: []string{}}
	for _, module := range enabled {
		job := s.moduleTask(c, tr, module, id, date, start, end, at, now)
		if tr.Mode == "interval" {
			var previous string
			if err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='job' AND id=?", job.ID).Scan(&previous); err == nil {
				var old heartbeat.Task
				if err = json.Unmarshal([]byte(previous), &old); err != nil {
					return err
				}
				job.History = old.History
			}
		}
		if err := job.Validate(); err != nil {
			return err
		}
		if err := saveEntity(ctx, tx, "job", job.ID, job); err != nil {
			return err
		}
		record.JobIDs = append(record.JobIDs, job.ID)
	}
	return saveEntity(ctx, tx, "module_occurrence", record.ID, record)
}

func moduleTriggerTime(tr ModuleTrigger, date string, start, now time.Time) (time.Time, string, bool) {
	at, key := start, tr.ID+":"+date
	if tr.Mode == "daily" || tr.Mode == "once" {
		if !now.Before(start.Add(time.Duration(tr.CatchUpSeconds) * time.Second)) {
			return at, key, false
		}
		if tr.Mode == "once" {
			key = tr.ID + ":once"
		}
	} else {
		interval := time.Duration(tr.IntervalSeconds) * time.Second
		n := int64(now.Sub(start) / interval)
		if n < 1 {
			return at, key, false
		}
		at = start.Add(time.Duration(n) * interval)
		key = tr.ID + ":interval"
	}
	return at, key, true
}

func reconcileModuleOccurrence(ctx context.Context, tx *sql.Tx, id string, revision int, tr ModuleTrigger, at, now time.Time) (bool, error) {
	var existing ModuleOccurrence
	var raw string
	err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='module_occurrence' AND id=?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = json.Unmarshal([]byte(raw), &existing); err != nil {
		return false, err
	}
	if tr.Mode == "interval" && at.Before(existing.ScheduledAt) {
		return true, nil
	}
	nextInterval := tr.Mode == "interval" && at.After(existing.ScheduledAt)
	frozen, busy, err := inspectOccurrenceJobs(ctx, tx, existing, tr, now)
	if err != nil {
		return false, err
	}
	if busy || (!nextInterval && (frozen || existing.Revision == revision)) {
		return true, nil
	}
	// Only unclaimed instances are reconciled; running instances keep a snapshot.
	for _, jobID := range existing.JobIDs {
		var jr string
		if err = tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='job' AND id=?", jobID).Scan(&jr); err == nil {
			var j heartbeat.Task
			if err = json.Unmarshal([]byte(jr), &j); err != nil {
				return false, err
			}
			j.Enabled = false
			if err = saveEntity(ctx, tx, "job", jobID, j); err != nil {
				return false, err
			}
		}
	}
	return false, nil
}

func inspectOccurrenceJobs(ctx context.Context, tx *sql.Tx, existing ModuleOccurrence, tr ModuleTrigger, now time.Time) (frozen, busy bool, err error) {
	for _, id := range existing.JobIDs {
		var raw string
		err = tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='job' AND id=?", id).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, false, err
		}
		var j heartbeat.Task
		if err = json.Unmarshal([]byte(raw), &j); err != nil {
			return false, false, err
		}
		if len(j.History) == 0 {
			continue
		}
		frozen = true
		last := j.History[len(j.History)-1]
		if last.Status == "running" || last.Status == "delivery_pending" {
			busy = true
		}
		if retryableModuleJob(tr, j, last, now) {
			j.Enabled, j.LastRunAt = true, 0
			if err = saveEntity(ctx, tx, "job", j.ID, j); err != nil {
				return false, false, err
			}
		}
	}
	return frozen, busy, nil
}

func retryableModuleJob(tr ModuleTrigger, j heartbeat.Task, last heartbeat.Run, now time.Time) bool {
	return (tr.Mode == "daily" || tr.Mode == "once") && (last.Status == "failed" || last.Status == "interrupted") && len(j.History) < 3 && now.UnixMilli()-last.StartedAt >= int64(len(j.History))*60000 && !j.Expired(now)
}

func (s HeartbeatService) moduleTask(c HeartbeatConfig, tr ModuleTrigger, id, occurrenceID, date string, start, end, at, now time.Time) heartbeat.Task {
	job := heartbeat.Task{ID: occurrenceID + "-" + id, Title: tr.Title + " / " + id, Prompt: "Host module invocation", At: at.Format(time.RFC3339), Timezone: c.Timezone, EndsAt: end.Format(time.RFC3339), Enabled: true, WorkspaceRoot: s.Options.Workspace, Model: s.Options.Model, ApprovalMode: "read-only", NewConversationEachRun: true, Target: s.Options.Target, CreatedAt: now.UnixMilli(), ModuleID: id, TriggerID: tr.ID, OccurrenceID: occurrenceID, TriggerRevision: c.Revision}
	switch id {
	case "weather_alert_check":
		job.Kind = "weather_check"
		job.Prompt = "Read official Shanghai alert sources with native independent-source reconciliation"
	case "github_pr_check":
		job.Kind = "module_check"
		job.Prompt = "Read public GitHub PR deltas using the host-owned native client"
	case "evening_briefing":
		job.Kind = "agent"
		job.Capability = "evening"
		job.DecideNotification = true
		job.Prompt = neutralInstanceInstructions(EveningInstructions)
		expiry := at.Add(10 * time.Minute)
		if expiry.Before(end) {
			job.EndsAt = expiry.Format(time.RFC3339)
		}
	case "ai_news_briefing":
		job.Kind = "agent"
		job.Capability = "news"
		job.DecideNotification = true
		job.Prompt = neutralInstanceInstructions(NewsBriefingInstructions)
		expires := at.Add(time.Duration(tr.CatchUpSeconds) * time.Second)
		if expires.Before(end) {
			job.EndsAt = expires.Format(time.RFC3339)
		}

	case "morning_briefing":
		job.Kind = "agent"
		job.Capability = "morning"
		job.DecideNotification = true
		job.Prompt = neutralInstanceInstructions(morningMailPolicy() + "\n" + MorningInstructions)
		expires := at.Add(time.Duration(tr.CatchUpSeconds) * time.Second)
		if expires.Before(end) {
			job.EndsAt = expires.Format(time.RFC3339)
		}
	case "course_briefing":
		job.Kind = "agent"
		job.Capability = "course"
		job.DecideNotification = true
		job.Prompt = neutralInstanceInstructions(CourseBriefingInstructions) + "\n宿主日期：" + date
		expires := at.Add(time.Duration(tr.CatchUpSeconds) * time.Second)
		if expires.Before(end) {
			job.EndsAt = expires.Format(time.RFC3339)
		}
	case "mail_digest":
		job.Kind = "agent"
		job.Capability = "mail"
		job.DecideNotification = true
		job.Prompt = neutralInstanceInstructions(MailDigestInstructions)
	case "weather_briefing":
		job.Kind = "agent"
		job.Capability = "weather"
		job.DecideNotification = true
		job.Prompt = neutralInstanceInstructions(WeatherMorningInstructions) + "\n宿主日期：" + date + "；本次Heartbeat触发时间：" + at.Format(time.RFC3339)
		expires := start.Add(time.Duration(tr.CatchUpSeconds) * time.Second)
		if (tr.Mode == "daily" || tr.Mode == "once") && expires.Before(end) {
			job.EndsAt = expires.Format(time.RFC3339)
		}
	case "mail_check":
		job.Kind = "mail_check"
		job.Prompt = "Sync mail delta from last successful cursor; native module, no model"
	}
	return job
}

func (s HeartbeatService) allowsOccurrence(ctx context.Context, t heartbeat.Task, module string) (bool, error) {
	var record ModuleOccurrence
	err := s.repo().transact(ctx, func(tx *sql.Tx) error {
		var raw string
		if err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='module_occurrence' AND id=?", t.OccurrenceID).Scan(&raw); err != nil {
			return err
		}
		return json.Unmarshal([]byte(raw), &record)
	})
	if err != nil {
		return false, err
	}
	return record.Revision == t.TriggerRevision && slices.Contains(record.Modules, module), nil
}
