package assistant

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"reasonix/internal/heartbeat"
)

func (s Tasks) change(todo *Todo, job *heartbeat.Task, in TaskRequest) error {
	now := s.clock().UTC()
	switch in.Action {
	case "complete":
		if todo.Status == "done" || todo.Status == "cancelled" {
			return errors.New("task is already closed")
		}
		if in.OccurrenceOnly {
			if job == nil || job.Interval == "" {
				return errors.New("occurrence_only applies only to recurring tasks")
			}
			loc, err := time.LoadLocation(job.Timezone)
			if err != nil {
				return err
			}
			local := now.In(loc)
			job.SkipUntil = time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, loc).Format(time.RFC3339)
			if len(job.History) > 0 && job.History[len(job.History)-1].Status == "delivery_pending" {
				job.History[len(job.History)-1].Status = "skipped"
			}
			todo.Completions = append(todo.Completions, now)
			if len(todo.Completions) > 30 {
				todo.Completions = todo.Completions[len(todo.Completions)-30:]
			}
			return nil
		}
		todo.Status = "done"
		if job != nil {
			job.Enabled = false
		}
	case "cancel":
		todo.Status = "cancelled"
		if job != nil {
			job.Enabled = false
		}
	case "pause":
		if todo.Status != "open" {
			return errors.New("only open tasks can be paused")
		}
		todo.Status = "paused"
		if job != nil {
			job.Enabled = false
		}
	case "resume":
		if todo.Status != "paused" {
			return errors.New("only paused tasks can be resumed")
		}
		todo.Status = "open"
		if job != nil {
			job.Enabled = true
		}
	case "update", "snooze":
		return s.changeSchedule(todo, job, in, now)
	default:
		return fmt.Errorf("unknown task action %q", in.Action)
	}
	return nil
}

func (s Tasks) changeSchedule(todo *Todo, job *heartbeat.Task, in TaskRequest, now time.Time) error {
	if todo.Status == "done" || todo.Status == "cancelled" {
		return errors.New("closed task cannot be edited; create a new task")
	}
	if busy(job) {
		return errors.New("task is executing or awaiting delivery; cancel/pause it or wait before rescheduling")
	}
	if in.Action == "snooze" {
		return snooze(todo, job, in, now)
	}
	if in.DueAt != "" {
		if in.DueAt == "never" {
			todo.DueAt = ""
		} else {
			if _, err := timestamp(in.DueAt); err != nil {
				return err
			}
			todo.DueAt = in.DueAt
		}
	}
	if in.Title != "" {
		if len(in.Title) > 512 {
			return errors.New("title exceeds 512 bytes")
		}
		if strings.TrimSpace(in.Title) == "" {
			return errors.New("title must not be blank")
		}
		if job != nil && todo.Body == "" {
			job.Prompt = strings.TrimSpace(in.Title)
		}
		todo.Title = strings.TrimSpace(in.Title)
		if job != nil {
			job.Title = todo.Title
		}
	}
	if in.Body != "" {
		if len(in.Body) > 16000 {
			return errors.New("body exceeds 16000 bytes")
		}
		todo.Body = in.Body
		if job != nil {
			job.Prompt = in.Body
		}
	}
	if in.At == "" && in.Interval == "" && in.EndsAt == "" && in.Timezone == "" {
		return nil
	}
	if job == nil {
		return errors.New("unscheduled todo cannot gain a schedule through update; create a reminder instead")
	}
	if in.At != "" {
		job.At = in.At
		job.Interval = ""
	}
	if in.Interval != "" {
		job.Interval = in.Interval
		job.At = ""
	}
	if in.EndsAt != "" {
		job.EndsAt = in.EndsAt
		if in.EndsAt == "never" {
			job.EndsAt = ""
		}
	}
	if in.Timezone != "" {
		job.Timezone = in.Timezone
	}
	job.LastRunAt = 0
	job.History = nil
	job.SkipUntil = ""
	job.SnoozedUntil = ""
	if err := checkFutureSchedule(*job, now); err != nil {
		return err
	}
	return job.Validate()
}

func snooze(todo *Todo, job *heartbeat.Task, in TaskRequest, now time.Time) error {

	if job == nil {
		return errors.New("todo has no reminder to snooze")
	}
	at, err := timestamp(in.At)
	if err != nil {
		return err
	}
	if at.IsZero() || !at.After(now) {
		return errors.New("snooze requires a future at time")
	}
	if job.Expired(at) {
		return errors.New("snooze time falls outside the task validity")
	}
	if job.Interval != "" {
		job.SnoozedUntil = in.At
	} else {
		job.At = in.At
		job.LastRunAt = 0
		job.History = nil
	}
	job.Enabled = true
	job.SkipUntil = ""
	todo.Status = "open"
	return nil

}

func busy(job *heartbeat.Task) bool {
	if job == nil || len(job.History) == 0 {
		return false
	}
	status := job.History[len(job.History)-1].Status
	return status == "running" || status == "delivery_pending"
}
