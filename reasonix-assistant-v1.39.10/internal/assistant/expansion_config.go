package assistant

import (
	"context"
	"database/sql"
)

// EnableExpansion applies the six owner-approved features without touching mail cursors or tasks.
func (s HeartbeatService) EnableExpansion(ctx context.Context) (HeartbeatConfig, error) {
	if e := allowMutation("update"); e != nil {
		return HeartbeatConfig{}, e
	}
	if _, e := s.Ensure(ctx); e != nil {
		return HeartbeatConfig{}, e
	}
	var result HeartbeatConfig
	e := s.repo().transact(ctx, func(tx *sql.Tx) error {
		c, e := loadHeartbeatConfig(ctx, tx)
		if e != nil {
			return e
		}
		for day, w := range c.Weekly {
			switch day {
			case "monday", "wednesday", "thursday":
				w.Start = "09:50"
			case "tuesday", "friday":
				w.Start = "08:00"
			case "saturday", "sunday":
				w.Start = "11:00"
			}
			w.End = "23:30"
			c.Weekly[day] = w
		}
		c.ModuleEnabled["evening_briefing"] = true
		c.ModuleEnabled["subscription_reminder"] = true
		c.Triggers["evening"] = ModuleTrigger{ID: "evening", Title: "23:00睡前总结", Enabled: true, Mode: "daily", Clock: "23:00", RoutineBound: true, Modules: []string{"evening_briefing"}, CatchUpSeconds: 300}
		c.Triggers["subscription_expiries"] = ModuleTrigger{ID: "subscription_expiries", Title: "订阅到期提醒", Enabled: true, Mode: "subscription", RoutineBound: true, Modules: []string{"subscription_reminder"}}
		c.Revision++
		if e = c.Validate(); e != nil {
			return e
		}
		if e = saveEntity(ctx, tx, "heartbeat_config", heartbeatConfigID, c); e != nil {
			return e
		}
		if e = saveEntity(ctx, tx, "assistant_policy", "expansion_v1", map[string]any{"deadline_lead_hours": []int{24, 2}, "overdue_followup": "evening_only", "subscription_lead_days": []int{7, 1, 0}, "mail_proposals_require_user_confirmation": true, "fixed_reminders_use_model_at_delivery": false}); e != nil {
			return e
		}
		result = c
		return nil
	})
	return result, e
}
