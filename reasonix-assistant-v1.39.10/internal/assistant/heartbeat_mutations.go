package assistant

import (
	"errors"
	"time"
)

func applyHeartbeatDate(c *HeartbeatConfig, r HeartbeatRequest, now time.Time) error {
	if _, e := time.Parse("2006-01-02", r.Date); e != nil {
		return errors.New("date must be YYYY-MM-DD")
	}
	if r.Action == "remove_date_override" {
		delete(c.Overrides, r.Date)
		return nil
	}
	if r.Date < now.In(mustShanghai()).Format("2006-01-02") {
		return errors.New("cannot change a past date")
	}
	if r.Start == "" && r.End == "" && r.Enabled == nil && r.Modules == nil {
		return errors.New("specify date fields to override")
	}
	o := c.Overrides[r.Date]
	if r.TriggerID != "" {
		if _, ok := c.Triggers[r.TriggerID]; !ok {
			return errors.New("unknown trigger")
		}
		if r.End != "" {
			return errors.New("per-trigger end override is not supported; set the routine end separately")
		}
		if r.Start != "" {
			if o.TriggerClocks == nil {
				o.TriggerClocks = map[string]string{}
			}
			o.TriggerClocks[r.TriggerID] = r.Start
			r.Start = ""
		}
		if r.Enabled != nil {
			if o.TriggerEnabled == nil {
				o.TriggerEnabled = map[string]bool{}
			}
			o.TriggerEnabled[r.TriggerID] = *r.Enabled
			r.Enabled = nil
		}
	}
	if r.Start != "" {
		o.Start = r.Start
	}
	if r.End != "" {
		o.End = r.End
	}
	if r.Enabled != nil {
		o.Enabled = r.Enabled
	}
	if r.Modules != nil {
		if _, ok := c.Triggers[r.TriggerID]; !ok {
			return errors.New("known trigger_id required for modules override")
		}
		if o.TriggerModules == nil {
			o.TriggerModules = map[string][]string{}
		}
		o.TriggerModules[r.TriggerID] = *r.Modules
	}
	c.Overrides[r.Date] = o
	return nil
}

func applyHeartbeatTrigger(c *HeartbeatConfig, r HeartbeatRequest, now time.Time) error {
	if r.TriggerID == "" {
		return errors.New("trigger_id required")
	}
	tr, ok := c.Triggers[r.TriggerID]
	if !ok {
		tr = ModuleTrigger{ID: r.TriggerID, Title: r.TriggerID, Enabled: true, Mode: "daily", RoutineBound: true, CatchUpSeconds: 3600}
	}
	if r.Clock != "" {
		tr.Clock = r.Clock
		tr.RoutineBound = false
	}
	if r.At != "" {
		at, e := time.Parse(time.RFC3339, r.At)
		if e != nil || !at.After(now) {
			return errors.New("at must be a future RFC3339 timestamp")
		}
		tr.At = r.At
		tr.Mode = "once"
		tr.RoutineBound = false
	}
	if r.Weekdays != nil {
		tr.Weekdays = *r.Weekdays
	}
	if r.RoutineBound != nil {
		tr.RoutineBound = *r.RoutineBound
	}
	if r.Title != "" {
		tr.Title = r.Title
	}
	if r.Enabled != nil {
		tr.Enabled = *r.Enabled
	}
	if r.Mode != "" {
		tr.Mode = r.Mode
	}
	if r.IntervalSeconds != 0 {
		tr.IntervalSeconds = r.IntervalSeconds
	}
	if r.CatchUpSeconds != 0 {
		tr.CatchUpSeconds = r.CatchUpSeconds
	}
	if r.Modules != nil {
		if r.Weekday != "" {
			if tr.WeekdayModules == nil {
				tr.WeekdayModules = map[string][]string{}
			}
			tr.WeekdayModules[r.Weekday] = *r.Modules
		} else {
			tr.Modules = *r.Modules
		}
	}
	if (tr.Mode == "daily" || tr.Mode == "once") && tr.CatchUpSeconds == 0 {
		tr.CatchUpSeconds = 3600
	}
	c.Triggers[r.TriggerID] = tr
	return nil
}
