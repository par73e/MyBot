package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reasonix/internal/heartbeat"
	"slices"
	"time"
)

func courseReminderText(v CourseInstance, lead int) string {
	return fmt.Sprintf("%s%d分钟后上%s，%s–%s，教室%s。", addressPrefix(), lead, v.Title, v.StartsAt.Format("15:04"), v.EndsAt.Format("15:04"), v.Room)
}
func (s HeartbeatService) prepareCourseTrigger(ctx context.Context, tx *sql.Tx, c HeartbeatConfig, tr ModuleTrigger, now time.Time) error {
	if !tr.Enabled || !c.ModuleEnabled["course_reminder"] {
		return nil
	}
	cfg, err := loadCourseConfig(ctx, tx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, day := range []time.Time{now, now.AddDate(0, 0, 1)} {
		for _, v := range cfg.Day(day).Courses {
			at := v.StartsAt.Add(-time.Duration(tr.LeadMinutes) * time.Minute)
			if now.Before(at) || !now.Before(at.Add(time.Duration(tr.CatchUpSeconds)*time.Second)) {
				continue
			}
			if !courseTriggerEnabled(c, tr, v, at) {
				continue
			}
			start, end, ok := c.Bounds(at.In(mustShanghai()).Format("2006-01-02"))
			if !ok || at.Before(start) || !at.Before(end) || !now.Before(end) {
				continue
			}
			if courseAtWake(c, v, at) {
				continue
			}
			if err = s.prepareCourseInstance(ctx, tx, c, tr, v, at, end, now); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s HeartbeatService) prepareCourseInstance(ctx context.Context, tx *sql.Tx, c HeartbeatConfig, tr ModuleTrigger, v CourseInstance, at, end, now time.Time) error {
	id := "hb-course-" + mailHash(tr.ID+":"+v.ID)
	var raw string
	err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='job' AND id=?", id).Scan(&raw)
	var old heartbeat.Task
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &old); err != nil {
			return err
		}
		if len(old.History) > 0 {
			return nil
		} // A claimed occurrence cannot be recreated by config edits/restarts.
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	deadline := at.Add(time.Duration(tr.CatchUpSeconds) * time.Second)
	if v.StartsAt.Before(deadline) {
		deadline = v.StartsAt
	}
	if end.Before(deadline) {
		deadline = end
	}
	occurrence := ModuleOccurrence{ID: id, TriggerID: tr.ID, Date: at.In(mustShanghai()).Format("2006-01-02"), Revision: c.Revision, ScheduledAt: at, Modules: []string{"course_reminder"}, JobIDs: []string{id}}
	job := heartbeat.Task{ID: id, Title: "上课提醒 / " + v.Title, Prompt: courseReminderText(v, tr.LeadMinutes), At: at.Format(time.RFC3339), EndsAt: deadline.Format(time.RFC3339), Enabled: true, Timezone: "Asia/Shanghai", WorkspaceRoot: s.Options.Workspace, ApprovalMode: "read-only", Kind: "message", Target: s.Options.Target, CreatedAt: now.UnixMilli(), ModuleID: "course_reminder", TriggerID: tr.ID, OccurrenceID: id, TriggerRevision: c.Revision, CourseID: v.CourseID, CourseDate: v.OriginalDate}
	if err = job.Validate(); err != nil {
		return err
	}
	if err = saveEntity(ctx, tx, "job", id, job); err != nil {
		return err
	}
	return saveEntity(ctx, tx, "module_occurrence", id, occurrence)
}
func (s HeartbeatService) courseAdmission(ctx context.Context, t heartbeat.Task, now time.Time) (bool, error) {
	c, err := s.Config(ctx)
	if err != nil {
		return false, err
	}
	tr, ok := c.Triggers[t.TriggerID]
	if !ok || !tr.Enabled || !c.ModuleEnabled["course_reminder"] || t.Expired(now) {
		return false, heartbeat.ErrOutsideRoutine
	}
	if !slices.Contains(c.Selected(tr, t.ScheduledTime(now).In(mustShanghai()).Format("2006-01-02")), "course_reminder") {
		return false, heartbeat.ErrOutsideRoutine
	}
	cfg, err := (CourseService{Home: s.Home}).Config(ctx)
	if err != nil {
		return false, err
	}
	original, err := courseDate(t.CourseDate)
	if err != nil {
		return false, err
	}
	for _, v := range cfg.Courses {
		if v.ID != t.CourseID || !cfg.matches(v, original) {
			continue
		}
		x, valid := cfg.instance(v, original)
		if !valid {
			return false, heartbeat.ErrOccurrenceChanged
		}
		at := x.StartsAt.Add(-time.Duration(tr.LeadMinutes) * time.Minute)
		if !courseTriggerEnabled(c, tr, x, at) {
			return false, heartbeat.ErrOutsideRoutine
		}
		if t.At != at.Format(time.RFC3339) || t.Prompt != courseReminderText(x, tr.LeadMinutes) {
			return false, heartbeat.ErrOccurrenceChanged
		}
		return true, nil
	}
	return false, heartbeat.ErrOccurrenceChanged
}

func courseTriggerEnabled(c HeartbeatConfig, tr ModuleTrigger, v CourseInstance, at time.Time) bool {
	date := at.In(mustShanghai()).Format("2006-01-02")
	if !tr.Enabled || !c.ModuleEnabled["course_reminder"] {
		return false
	}
	if enabled, ok := c.Overrides[date].TriggerEnabled[tr.ID]; ok && !enabled {
		return false
	}
	if len(tr.Weekdays) > 0 && !slices.Contains(tr.Weekdays, weatherWeekdays[v.StartsAt.Weekday()]) {
		return false
	}
	return slices.Contains(c.Selected(tr, date), "course_reminder")
}
