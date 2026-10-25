package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"reasonix/internal/heartbeat"
)

const heartbeatConfigID = "default"

// Modules own execution contracts, never clocks or user-selectable prompts/paths.
type ModuleDefinition struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Version   int    `json:"version"`
	Execution string `json:"execution"`
}

func HeartbeatModules() []ModuleDefinition {
	return []ModuleDefinition{{"evening_briefing", "睡前总结", 1, "native_then_agent"}, {"subscription_reminder", "订阅到期提醒", 1, "native"}, {"weather_briefing", "详细天气播报", 1, "agent"}, {"mail_check", "邮箱增量检查及新邮件汇报", 2, "native_then_agent"}, {"mail_digest", "静默邮件汇总", 1, "agent"}, {"course_briefing", "今日课程简报", 1, "agent"}, {"course_reminder", "课前提醒", 1, "native"}, {"morning_briefing", "早安Friday", 1, "native_then_agent"}, {"ai_news_briefing", "AI News热点新闻", 1, "native_then_agent"}}
}
func knownModule(id string) bool {
	for _, m := range HeartbeatModules() {
		if m.ID == id {
			return true
		}
	}
	return false
}

type ModuleTrigger struct {
	LeadMinutes     int                 `json:"lead_minutes,omitempty"`
	Clock           string              `json:"clock,omitempty"`
	At              string              `json:"at,omitempty"`
	Weekdays        []string            `json:"weekdays,omitempty"`
	RoutineBound    bool                `json:"routine_bound"`
	ID              string              `json:"id"`
	Title           string              `json:"title"`
	Enabled         bool                `json:"enabled"`
	Mode            string              `json:"mode"` // daily once, or interval within the shared routine
	Modules         []string            `json:"modules"`
	WeekdayModules  map[string][]string `json:"weekday_modules,omitempty"`
	IntervalSeconds int                 `json:"interval_seconds,omitempty"`
	CatchUpSeconds  int                 `json:"catch_up_seconds,omitempty"`
}
type HeartbeatOverride struct {
	TriggerClocks  map[string]string   `json:"trigger_clocks,omitempty"`
	TriggerEnabled map[string]bool     `json:"trigger_enabled,omitempty"`
	Start          string              `json:"start,omitempty"`
	End            string              `json:"end,omitempty"`
	Enabled        *bool               `json:"enabled,omitempty"`
	TriggerModules map[string][]string `json:"trigger_modules,omitempty"`
}
type HeartbeatConfig struct {
	Revision      int                          `json:"revision"`
	Timezone      string                       `json:"timezone"`
	Weekly        heartbeat.WeeklySchedule     `json:"weekly_schedule"`
	Overrides     map[string]HeartbeatOverride `json:"date_overrides"`
	Triggers      map[string]ModuleTrigger     `json:"triggers"`
	ModuleEnabled map[string]bool              `json:"module_enabled"`
}
type HeartbeatService struct {
	Home    string
	Options Options
}

func (s HeartbeatService) repo() Repository { return NewRepository(s.Home) }
func loadHeartbeatConfig(ctx context.Context, tx *sql.Tx) (HeartbeatConfig, error) {
	var c HeartbeatConfig
	var raw string
	err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='heartbeat_config' AND id=?", heartbeatConfigID).Scan(&raw)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal([]byte(raw), &c)
	if err == nil {
		err = c.Validate()
	}
	return c, err
}
func (s HeartbeatService) Config(ctx context.Context) (HeartbeatConfig, error) {
	var c HeartbeatConfig
	err := s.repo().transact(ctx, func(tx *sql.Tx) error { var e error; c, e = loadHeartbeatConfig(ctx, tx); return e })
	return c, err
}
func validateModuleList(ids []string) error {
	if len(ids) == 0 || len(ids) > len(HeartbeatModules()) {
		return errors.New("select at least one known module, without duplicates")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !knownModule(id) || seen[id] {
			return fmt.Errorf("unknown or duplicate module %q", id)
		}
		seen[id] = true
	}
	return nil
}
func (c HeartbeatConfig) Validate() error {
	if c.Revision < 1 || c.Timezone != "Asia/Shanghai" {
		return errors.New("heartbeat requires a revision and Asia/Shanghai timezone")
	}
	if err := c.Weekly.Validate(); err != nil {
		return err
	}
	// Civil-day windows have one unambiguous end; 24:00 represents next midnight.
	for _, w := range c.Weekly {
		if err := (heartbeat.DateOverrides{"2000-01-01": w}).Validate(); err != nil {
			return err
		}
	}
	if len(c.Triggers) > 32 || len(c.Triggers) == 0 {
		return errors.New("heartbeat requires 1..32 triggers")
	}
	validDays := map[string]bool{}
	for _, d := range weatherWeekdays {
		validDays[d] = true
	}
	for id, t := range c.Triggers {
		if err := validateModuleTrigger(id, t, validDays); err != nil {
			return err
		}
	}
	for _, m := range HeartbeatModules() {
		if _, ok := c.ModuleEnabled[m.ID]; !ok {
			// v20 configurations predate the optional digest module. Missing
			// means disabled until Ensure adds the catalog flag once.
			if m.ID == "mail_digest" || m.ID == "course_briefing" || m.ID == "course_reminder" || m.ID == "morning_briefing" || m.ID == "ai_news_briefing" || m.ID == "evening_briefing" || m.ID == "subscription_reminder" {
				continue
			}
			return errors.New("missing module enable state")
		}
	}
	for id := range c.ModuleEnabled {
		if !knownModule(id) {
			return errors.New("unknown module enable flag")
		}
	}
	if len(c.Overrides) > 366 {
		return errors.New("too many date overrides")
	}
	for date, o := range c.Overrides {
		if err := c.validateDateOverride(date, o); err != nil {
			return err
		}
	}
	return nil
}
func (c HeartbeatConfig) Window(date string) (heartbeat.DailyWindow, error) {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return heartbeat.DailyWindow{}, err
	}
	w := c.Weekly[weatherWeekdays[d.Weekday()]]
	o := c.Overrides[date]
	if o.Start != "" {
		w.Start = o.Start
	}
	if o.End != "" {
		w.End = o.End
	}
	if o.Enabled != nil {
		w.Enabled = *o.Enabled
	}
	return w, nil
}
func (c HeartbeatConfig) Bounds(date string) (time.Time, time.Time, bool) {
	w, err := c.Window(date)
	if err != nil || !w.Enabled {
		return time.Time{}, time.Time{}, false
	}
	z, _ := time.LoadLocation(c.Timezone)
	start, e := time.ParseInLocation("2006-01-02 15:04", date+" "+w.Start, z)
	if e != nil {
		return time.Time{}, time.Time{}, false
	}
	var end time.Time
	if w.End == "24:00" {
		d, _ := time.ParseInLocation("2006-01-02", date, z)
		end = d.AddDate(0, 0, 1)
	} else {
		end, e = time.ParseInLocation("2006-01-02 15:04", date+" "+w.End, z)
	}
	return start, end, e == nil && start.Before(end)
}
func (c HeartbeatConfig) Selected(t ModuleTrigger, date string) []string {
	d, _ := time.Parse("2006-01-02", date)
	mods := t.Modules
	if v, ok := t.WeekdayModules[weatherWeekdays[d.Weekday()]]; ok {
		mods = v
	}
	if v, ok := c.Overrides[date].TriggerModules[t.ID]; ok {
		mods = v
	}
	return append([]string(nil), mods...)
}

// Ensure migrates the two existing clock sources once, preserving owner state.
func (s HeartbeatService) Ensure(ctx context.Context) (HeartbeatConfig, error) {
	var result HeartbeatConfig
	err := s.repo().transact(ctx, func(tx *sql.Tx) error {
		c, e := loadHeartbeatConfig(ctx, tx)
		if e == nil {
			if upgradeHeartbeatCatalog(&c) {
				c.Revision++
				if err := saveEntity(ctx, tx, "heartbeat_config", heartbeatConfigID, c); err != nil {
					return err
				}
			}
			result = c
			return c.Validate()
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		c = HeartbeatConfig{Revision: 1, Timezone: "Asia/Shanghai", Weekly: heartbeat.WeeklySchedule{}, Overrides: map[string]HeartbeatOverride{}, Triggers: map[string]ModuleTrigger{}, ModuleEnabled: map[string]bool{}}
		var raw string
		var old heartbeat.Task
		if e = tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='job' AND id=?", MailScheduleID).Scan(&raw); e == nil {
			if e = json.Unmarshal([]byte(raw), &old); e != nil {
				return e
			}
		}
		if len(old.WeeklySchedule) > 0 {
			c.Weekly = old.WeeklySchedule
		} else {
			for _, day := range weatherWeekdays {
				start, end := "08:00", "24:00"
				if old.TimeWindowStart != "" {
					start = old.TimeWindowStart
				}
				if old.TimeWindowEnd != "" && old.TimeWindowEnd != "00:00" {
					end = old.TimeWindowEnd
				}
				c.Weekly[day] = heartbeat.DailyWindow{Enabled: true, Start: start, End: end}
			}
		}
		weather, werr := NewWeatherService(s.Home).Config()
		if werr == nil {
			for day, w := range c.Weekly {
				if len(old.WeeklySchedule) > 0 && weather.WakeTimes[day] != w.Start {
					return errors.New("mail/weather wake defaults conflict; resolve before migration")
				}
				w.Start = weather.WakeTimes[day]
				c.Weekly[day] = w
			}
		}
		_, merr := NewMailService(s.Home).Config()
		c.ModuleEnabled["mail_check"] = merr == nil && (old.ID == "" || old.Enabled)
		c.ModuleEnabled["mail_digest"] = true
		c.ModuleEnabled["course_briefing"] = false
		c.ModuleEnabled["course_reminder"] = false
		c.ModuleEnabled["morning_briefing"] = false
		c.ModuleEnabled["ai_news_briefing"] = false
		c.ModuleEnabled["evening_briefing"] = false
		c.ModuleEnabled["subscription_reminder"] = false
		c.ModuleEnabled["weather_briefing"] = werr == nil && weather.MorningEnabled
		for date, w := range old.DateOverrides {
			enabled := w.Enabled
			c.Overrides[date] = HeartbeatOverride{Start: w.Start, End: w.End, Enabled: &enabled}
		}
		c.Triggers["morning"] = ModuleTrigger{ID: "morning", Title: "起床时运行的模块", Enabled: true, Mode: "daily", RoutineBound: true, Modules: []string{"weather_briefing", "mail_check"}, CatchUpSeconds: 3600}
		c.Triggers["daytime_mail"] = ModuleTrigger{ID: "daytime_mail", Title: "白天重复邮箱检查", Enabled: true, Mode: "interval", RoutineBound: true, Modules: []string{"mail_check"}, IntervalSeconds: 60}
		if d, e := time.ParseDuration(old.Interval); e == nil && d >= 60*time.Second && d <= 24*time.Hour {
			t := c.Triggers["daytime_mail"]
			t.IntervalSeconds = int(d / time.Second)
			c.Triggers[t.ID] = t
		}
		if err := c.Validate(); err != nil {
			return err
		}
		if err := saveEntity(ctx, tx, "heartbeat_config", heartbeatConfigID, c); err != nil {
			return err
		}
		for _, id := range []string{MailScheduleID, "weather-morning-check"} {
			if e = tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='job' AND id=?", id).Scan(&raw); e == nil {
				var j heartbeat.Task
				if e = json.Unmarshal([]byte(raw), &j); e != nil {
					return e
				}
				j.Enabled = false
				if e = saveEntity(ctx, tx, "job", id, j); e != nil {
					return e
				}
			} else if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
		}
		result = c
		return nil
	})
	return result, err
}

// TriggerBounds resolves the configured schedule, never a module-owned time.
func (c HeartbeatConfig) TriggerBounds(tr ModuleTrigger, date string) (time.Time, time.Time, bool) {
	day, e := time.ParseInLocation("2006-01-02", date, mustShanghai())
	if e != nil || !tr.Enabled {
		return time.Time{}, time.Time{}, false
	}
	if enabled, ok := c.Overrides[date].TriggerEnabled[tr.ID]; ok && !enabled {
		return time.Time{}, time.Time{}, false
	}
	if len(tr.Weekdays) > 0 {
		match := false
		for _, d := range tr.Weekdays {
			if d == weatherWeekdays[day.Weekday()] {
				match = true
			}
		}
		if !match {
			return time.Time{}, time.Time{}, false
		}
	}
	start, end, ok := c.Bounds(date)
	if !ok {
		return start, end, false
	}
	if tr.Mode == "once" {
		at, e := time.Parse(time.RFC3339, tr.At)
		if e != nil || at.In(mustShanghai()).Format("2006-01-02") != date {
			return start, end, false
		}
		if at.Before(start) || !at.Before(end) {
			return start, end, false
		}
		start = at
	}
	clock := tr.Clock
	if v, ok := c.Overrides[date].TriggerClocks[tr.ID]; ok {
		clock = v
	}
	if clock != "" {
		at, e := time.ParseInLocation("2006-01-02 15:04", date+" "+clock, mustShanghai())
		if e != nil || at.Before(start) {
			return start, end, false
		}
		start = at
	}
	return start, end, start.Before(end)
}

func upgradeHeartbeatCatalog(c *HeartbeatConfig) bool {
	changed := false
	for _, m := range HeartbeatModules() {
		if _, exists := c.ModuleEnabled[m.ID]; !exists {
			c.ModuleEnabled[m.ID] = m.ID == "mail_digest"
			changed = true
		}
	}
	for id, tr := range c.Triggers {
		if !tr.RoutineBound {
			tr.RoutineBound = true
			c.Triggers[id] = tr
			changed = true
		}
	}
	return changed
}
