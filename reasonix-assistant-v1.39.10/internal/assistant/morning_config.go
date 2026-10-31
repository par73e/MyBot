package assistant

import (
	"context"
	"database/sql"
	"reasonix/internal/heartbeat"
	"slices"
	"time"
)

// EnableMorning replaces the wake selection; underlying modules and daytime polling survive.
func (s HeartbeatService) EnableMorning(ctx context.Context) (HeartbeatConfig, error) {
	if err := allowMutation("update"); err != nil {
		return HeartbeatConfig{}, err
	}
	if _, err := s.Ensure(ctx); err != nil {
		return HeartbeatConfig{}, err
	}
	var result HeartbeatConfig
	err := s.repo().transact(ctx, func(tx *sql.Tx) error {
		c, e := loadHeartbeatConfig(ctx, tx)
		if e != nil {
			return e
		}
		tr := c.Triggers["morning"]
		tr.ID = "morning"
		tr.Title = neutralInstanceInstructions("早安Friday")
		tr.Mode = "daily"
		tr.LeadMinutes = 0
		tr.IntervalSeconds = 0
		tr.Weekdays = nil
		tr.Clock = ""
		tr.At = ""
		tr.Enabled = true
		tr.RoutineBound = true
		tr.Modules = []string{"morning_briefing"}
		tr.WeekdayModules = nil
		tr.CatchUpSeconds = 300
		c.Triggers["morning"] = tr
		c.ModuleEnabled["morning_briefing"] = true
		for date, o := range c.Overrides {
			delete(o.TriggerModules, "morning")
			c.Overrides[date] = o
		}
		c.Revision++
		if e = c.Validate(); e != nil {
			return e
		}
		if e = saveEntity(ctx, tx, "heartbeat_config", heartbeatConfigID, c); e != nil {
			return e
		}
		jobs, e := loadEntities[heartbeat.Task](ctx, tx, "job")
		if e != nil {
			return e
		}
		for _, j := range jobs {
			if j.TriggerID == "morning" && j.ModuleID != "morning_briefing" && j.Enabled {
				j.Enabled = false
				if e = saveEntity(ctx, tx, "job", j.ID, j); e != nil {
					return e
				}
			}
		}
		result = c
		return nil
	})
	return result, err
}
func morningOwnsWake(c HeartbeatConfig, date string) bool {
	tr, ok := c.Triggers["morning"]
	if !ok || !tr.Enabled || !c.ModuleEnabled["morning_briefing"] {
		return false
	}
	if enabled, ok := c.Overrides[date].TriggerEnabled["morning"]; ok && !enabled {
		return false
	}
	wake, _, open := c.Bounds(date)
	at, _, valid := c.TriggerBounds(tr, date)
	return open && valid && at.Equal(wake) && slices.Contains(c.Selected(tr, date), "morning_briefing")
}
func courseAtWake(c HeartbeatConfig, v CourseInstance, at time.Time) bool {
	date := at.In(mustShanghai()).Format("2006-01-02")
	start, _, ok := c.Bounds(date)
	return ok && at.Equal(start) && morningOwnsWake(c, date)
}
