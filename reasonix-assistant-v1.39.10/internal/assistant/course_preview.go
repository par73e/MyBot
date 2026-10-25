package assistant

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CoursePreview explains absent events without creating jobs or notifications.
func (s CourseService) CoursePreview(ctx context.Context, c HeartbeatConfig, date string) (any, error) {
	day, err := courseDate(date)
	if err != nil {
		return nil, err
	}
	cfg, err := s.Config(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{"configured": false}, nil
	}
	if err != nil {
		return nil, err
	}
	events := []map[string]any{}
	for _, tr := range c.Triggers {
		if tr.Mode != "course" {
			continue
		}
		for _, v := range cfg.Day(day).Courses {
			at := v.StartsAt.Add(-time.Duration(tr.LeadMinutes) * time.Minute)
			start, end, ok := c.Bounds(at.Format("2006-01-02"))
			reason := "eligible"
			if !courseTriggerEnabled(c, tr, v, at) {
				reason = "disabled"
			} else if !ok || at.Before(start) || !at.Before(end) {
				reason = "outside_routine"
			}
			events = append(events, map[string]any{"trigger_id": tr.ID, "course_id": v.ID, "title": v.Title, "room": v.Room, "starts_at": v.StartsAt, "scheduled_at": at, "reason": reason})
		}
	}
	return map[string]any{"configured": true, "date": date, "week": cfg.Week(day), "events": events}, nil
}
