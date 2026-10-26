package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"time"
)

func alertLoad(ctx context.Context, tx *sql.Tx, kind, id string, out any) error {
	var raw string
	if err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind=? AND id=?", kind, id).Scan(&raw); err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), out)
}
func alertRead[T any](ctx context.Context, r Repository, kind, id string) (T, error) {
	var v T
	err := r.transact(ctx, func(tx *sql.Tx) error { return alertLoad(ctx, tx, kind, id, &v) })
	return v, err
}
func (s AlertService) Config(ctx context.Context) (AlertConfig, error) {
	c, err := alertRead[AlertConfig](ctx, s.repo(), "alert_config", "default")
	if errors.Is(err, sql.ErrNoRows) {
		return AlertConfig{Region: "上海市", PollSeconds: 300}, nil
	}
	return c, err
}
func (s AlertService) Apply(ctx context.Context, r AlertRequest) (any, error) {
	if r.Action == "status" {
		return s.Status(ctx)
	}
	if r.Action != "configure" {
		return nil, errors.New("expected status or configure")
	}
	if os.Getenv("REASONIX_ASSISTANT_BACKGROUND") == "1" {
		return nil, errors.New("only Sir's foreground request may configure alerts")
	}
	if r.PollSeconds != nil && (*r.PollSeconds < 300 || *r.PollSeconds > 3600 || *r.PollSeconds%60 != 0) {
		return nil, errors.New("poll_seconds must be 300..3600 in whole minutes")
	}
	if _, err := (HeartbeatService{Home: s.Home}).Ensure(ctx); err != nil {
		return nil, err
	}
	var result AlertConfig
	err := s.repo().transact(ctx, func(tx *sql.Tx) error {
		c := AlertConfig{Region: "上海市", PollSeconds: 300}
		if err := alertLoad(ctx, tx, "alert_config", "default", &c); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if c.Revision != r.Revision {
			return errors.New("alert configuration changed; read status before configuring")
		}
		if r.Enabled != nil {
			c.Enabled = *r.Enabled
		}
		if r.PollSeconds != nil {
			c.PollSeconds = *r.PollSeconds
		}
		c.Revision++
		hb, err := loadHeartbeatConfig(ctx, tx)
		if err != nil {
			return err
		}
		tr := ModuleTrigger{ID: "weather_alerts", Title: "上海官方天气预警", Mode: "interval", IntervalSeconds: c.PollSeconds, RoutineBound: true, Modules: []string{"weather_alert_check"}, Enabled: c.Enabled}
		hb.Triggers[tr.ID] = tr
		hb.ModuleEnabled["weather_alert_check"] = c.Enabled
		hb.Revision++
		if err = hb.Validate(); err != nil {
			return err
		}
		if err = saveEntity(ctx, tx, "heartbeat_config", heartbeatConfigID, hb); err != nil {
			return err
		}
		result = c
		return saveEntity(ctx, tx, "alert_config", "default", c)
	})
	return result, err
}
func alertWindow(c HeartbeatConfig, now time.Time) bool {
	tr, ok := c.Triggers["weather_alerts"]
	date := now.In(mustShanghai()).Format("2006-01-02")
	if !ok || !tr.Enabled || !c.ModuleEnabled["weather_alert_check"] || !slices.Contains(c.Selected(tr, date), "weather_alert_check") {
		return false
	}
	start, end, open := c.TriggerBounds(tr, date)
	return open && !now.Before(start) && now.Before(end)
}
