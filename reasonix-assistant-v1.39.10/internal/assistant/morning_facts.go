package assistant

import (
	"context"
	"database/sql"
	"errors"
	"reasonix/internal/heartbeat"
	"time"
)

func loadMorningFacts(ctx context.Context, tx *sql.Tx, v *MorningSnapshot, cfg HeartbeatConfig, now time.Time) error {
	v.CourseStatus = "unavailable"
	v.Courses = CourseDay{}
	v.WakeCourses = nil
	c, e := loadCourseConfig(ctx, tx)
	if e == nil {
		v.Courses = c.Day(now)
		v.CourseStatus = "ok"
		for _, tr := range cfg.Triggers {
			if tr.Mode != "course" || !tr.Enabled || !cfg.ModuleEnabled["course_reminder"] {
				continue
			}
			for _, course := range v.Courses.Courses {
				at := course.StartsAt.Add(-time.Duration(tr.LeadMinutes) * time.Minute)
				if courseTriggerEnabled(cfg, tr, course, at) && courseAtWake(cfg, course, at) {
					v.WakeCourses = append(v.WakeCourses, course)
				}
			}
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	todos, e := loadEntities[Todo](ctx, tx, "todo")
	if e != nil {
		return e
	}
	jobs, e := loadEntities[heartbeat.Task](ctx, tx, "job")
	if e != nil {
		return e
	}
	planned := map[string]bool{}
	v.ReminderTimes = map[string]string{}
	wanted := map[string]bool{}
	for _, todo := range todos {
		wanted[todo.JobID] = true
	}
	for _, j := range jobs {
		if wanted[j.ID] && j.ScheduledOn(now) {
			planned[j.ID] = true
			if j.Enabled && j.At != "" && j.SnoozedUntil == "" {
				v.ReminderTimes[j.ID] = j.At
			}
		}
	}
	for i := range todos {
		for j := range jobs {
			if jobs[j].ID == todos[i].JobID {
				todos[i] = notifiedReminderView(todos[i], &jobs[j])
				break
			}
		}
	}
	v.Todos = morningTodos(todos, now, planned)
	return loadOvernightMail(ctx, tx, v, now)

}
func (s MorningService) refreshFacts(ctx context.Context, v *MorningSnapshot) error {
	cfg, e := (HeartbeatService{Home: s.Home}).Config(ctx)
	if e != nil {
		return e
	}
	day, e := courseDate(v.Date)
	if e != nil {
		return e
	}
	return NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		if e := loadMorningFacts(ctx, tx, v, cfg, day); e != nil {
			return e
		}
		return saveEntity(ctx, tx, "morning_snapshot", v.ID, v)
	})
}

// Wording must not claim an empty night just because daytime polling already handled its mail.
func loadOvernightMail(ctx context.Context, tx *sql.Tx, v *MorningSnapshot, day time.Time) error {
	records, err := loadEntities[mailRecord](ctx, tx, "mail_message")
	if err != nil {
		return err
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, mustShanghai()).Add(-6 * time.Hour)
	v.OvernightMailSeen = false
	for _, r := range records {
		at := time.Unix(r.Message.ReceivedAt, 0)
		if !at.Before(start) && !at.After(v.CreatedAt) {
			v.OvernightMailSeen = true
			break
		}
	}
	return nil
}
