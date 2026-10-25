package assistant

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"reasonix/internal/heartbeat"
)

func (s Tasks) deadlineJobs(ctx context.Context, tx *sql.Tx, v *TaskView, now time.Time) error {
	if !v.DeadlineReminders || v.Status != "open" || v.DueAt == "" {
		return nil
	}
	if s.Options.Target == nil {
		return errors.New("deadline reminder destination is not configured")
	}
	due, err := timestamp(v.DueAt)
	if err != nil {
		return err
	}
	c, err := loadHeartbeatConfig(ctx, tx)
	if err != nil {
		return err
	}
	for _, hours := range []int{24, 2} {
		at := due.Add(-time.Duration(hours) * time.Hour)
		label := at.In(mustShanghai()).Format("2006-01-02 15:04")
		if !at.After(now) {
			v.ReminderWarnings = append(v.ReminderWarnings, label+"已过，不补发")
			continue
		}
		start, end, ok := c.Bounds(at.In(mustShanghai()).Format("2006-01-02"))
		if !ok || at.Before(start) || !at.Before(end) {
			v.ReminderWarnings = append(v.ReminderWarnings, label+"在作息外，跳过且不顺延")
			continue
		}
		id := fmt.Sprintf("deadline-%s-%d-%d", v.ID, v.ReminderRevision, hours)
		deadline := at.Add(10 * time.Minute)
		if end.Before(deadline) {
			deadline = end
		}
		job := heartbeat.Task{ID: id, Title: "截止提醒 / " + v.Title, Prompt: fmt.Sprintf("Sir，记得处理「%s」，%s截止。", singleLine(v.Title), due.In(mustShanghai()).Format("1月2日15:04")), At: at.Format(time.RFC3339), EndsAt: deadline.Format(time.RFC3339), Timezone: "Asia/Shanghai", Enabled: true, WorkspaceRoot: s.Options.Workspace, Model: s.Options.Model, ApprovalMode: "read-only", Kind: "message", Target: s.Options.Target, CreatedAt: now.UnixMilli()}
		if err = job.Validate(); err != nil {
			return err
		}
		v.ReminderJobIDs = append(v.ReminderJobIDs, id)
		v.Reminders = append(v.Reminders, job)
	}
	return nil
}

func (s Tasks) refreshDeadlineJobs(ctx context.Context, tx *sql.Tx, old Todo, v *TaskView, in TaskRequest, jobs []heartbeat.Task) error {
	changed := old.DueAt != v.DueAt || old.Title != v.Title || old.Body != v.Body || in.DeadlineReminders != nil || in.Action == "resume"
	if in.DeadlineReminders != nil {
		v.DeadlineReminders = *in.DeadlineReminders
	}
	if !changed && v.Status == "open" {
		attachReminderJobs(v, jobs)
		return nil
	}
	for _, job := range jobs {
		if !slices.Contains(old.ReminderJobIDs, job.ID) {
			continue
		}
		job.Enabled = false
		if err := saveEntity(ctx, tx, "job", job.ID, job); err != nil {
			return err
		}
	}
	if !changed {
		attachReminderJobs(v, jobs)
		for i := range v.Reminders {
			v.Reminders[i].Enabled = false
		}
		return nil
	}
	v.ReminderRevision++
	v.ReminderJobIDs = nil
	return s.deadlineJobs(ctx, tx, v, s.clock())
}

func attachReminderJobs(v *TaskView, jobs []heartbeat.Task) {
	for _, j := range jobs {
		if slices.Contains(v.ReminderJobIDs, j.ID) {
			v.Reminders = append(v.Reminders, j)
		}
	}
}

func personalTaskAdmission(ctx context.Context, repo Repository, t heartbeat.Task) (bool, error) {
	if !strings.HasPrefix(t.ID, "todo-") && !strings.HasPrefix(t.ID, "deadline-") {
		return true, nil
	}
	valid := false
	err := repo.transact(ctx, func(tx *sql.Tx) error {
		todos, err := loadEntities[Todo](ctx, tx, "todo")
		if err != nil {
			return err
		}
		for _, v := range todos {
			if v.JobID == t.ID || slices.Contains(v.ReminderJobIDs, t.ID) {
				if v.Status != "open" {
					return nil
				}
				var raw string
				if err = tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='job' AND id=?", t.ID).Scan(&raw); err != nil {
					return err
				}
				var live heartbeat.Task
				if err = decode([]byte(raw), &live); err != nil {
					return err
				}
				valid = live.Enabled && live.At == t.At && live.Interval == t.Interval && live.Prompt == t.Prompt
				return nil
			}
		}
		return nil
	})
	if err == nil && !valid {
		return false, heartbeat.ErrOccurrenceChanged
	}
	return valid, err
}
