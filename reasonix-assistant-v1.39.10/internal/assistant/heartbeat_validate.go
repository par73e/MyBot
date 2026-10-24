package assistant

import (
	"errors"
	"reasonix/internal/heartbeat"
	"strings"
	"time"
)

func validateModuleTrigger(id string, t ModuleTrigger, validDays map[string]bool) error {
	if id != t.ID || id == "" || len(id) > 64 || strings.ContainsAny(id, " /\\") || t.Title == "" {
		return errors.New("invalid trigger identity")
	}
	if err := validateModuleList(t.Modules); err != nil {
		return err
	}
	if t.Mode != "daily" && t.Mode != "interval" && t.Mode != "once" {
		return errors.New("trigger mode must be daily, interval or once")
	}
	if t.Mode == "interval" && (t.IntervalSeconds < 60 || t.IntervalSeconds > 86400) {
		return errors.New("interval_seconds must be 60..86400")
	}
	if (t.Mode == "daily" || t.Mode == "once") && (t.CatchUpSeconds < 60 || t.CatchUpSeconds > 86400) {
		return errors.New("daily catch_up_seconds must be 60..86400")
	}
	if t.Mode == "once" {
		if t.Clock != "" {
			return errors.New("once trigger cannot also have a daily clock")
		}
		if _, e := time.Parse(time.RFC3339, t.At); e != nil {
			return errors.New("once trigger needs RFC3339 at")
		}
	} else if t.At != "" {
		return errors.New("at only valid for once triggers")
	}
	if t.Clock != "" {
		if _, e := time.Parse("15:04", t.Clock); e != nil {
			return errors.New("clock must be HH:MM")
		}
	}
	for _, day := range t.Weekdays {
		if !validDays[day] {
			return errors.New("invalid trigger weekday")
		}
	}
	for day, mods := range t.WeekdayModules {
		if !validDays[day] {
			return errors.New("invalid weekday")
		}
		if err := validateModuleList(mods); err != nil {
			return err
		}
	}
	return nil
}

func (c HeartbeatConfig) validateDateOverride(date string, o HeartbeatOverride) error {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return errors.New("override date must be YYYY-MM-DD")
	}
	w := c.Weekly[weatherWeekdays[d.Weekday()]]
	if o.Start != "" {
		w.Start = o.Start
	}
	if o.End != "" {
		w.End = o.End
	}
	if o.Enabled != nil {
		w.Enabled = *o.Enabled
	}
	if !w.Enabled {
		w.Start = ""
		w.End = ""
	}
	if err := (heartbeat.DateOverrides{date: w}).Validate(); err != nil {
		return err
	}
	for id, clock := range o.TriggerClocks {
		if _, ok := c.Triggers[id]; !ok {
			return errors.New("unknown trigger in date clock")
		}
		if _, e := time.Parse("15:04", clock); e != nil {
			return errors.New("date clock must use HH:MM")
		}
	}
	for id := range o.TriggerEnabled {
		if _, ok := c.Triggers[id]; !ok {
			return errors.New("unknown trigger in date enable state")
		}
	}
	for id, mods := range o.TriggerModules {
		if _, ok := c.Triggers[id]; !ok {
			return errors.New("override references unknown trigger")
		}
		if err := validateModuleList(mods); err != nil {
			return err
		}
	}
	return nil
}
