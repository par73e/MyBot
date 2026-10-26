package assistant

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"reasonix/internal/heartbeat"
)

func (s AlertService) queueAlert(ctx context.Context, tx *sql.Tx, e AlertEvent, o Options) error {
	if o.Target == nil || e.Superseded || !e.DeliveredAt.IsZero() {
		return nil
	}
	var c AlertConfig
	if err := alertLoad(ctx, tx, "alert_config", "default", &c); err != nil {
		return err
	}
	hb, err := loadHeartbeatConfig(ctx, tx)
	if err != nil {
		return err
	}
	if !c.Enabled || !alertWindow(hb, s.now()) || (!e.Alert.Cancel && alertExpired(e.Alert, s.now())) {
		return nil
	}
	if e.JobID != "" {
		var old heartbeat.Task
		err = alertLoad(ctx, tx, "job", e.JobID, &old)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if old.Enabled && !old.Expired(s.now()) {
			return nil
		}
		if len(old.History) > 0 {
			switch old.History[len(old.History)-1].Status {
			case "delivered", "delivery_uncertain", "delivery_failed":
				return nil
			}
		}
	}
	date := s.now().In(mustShanghai()).Format("2006-01-02")
	_, end, _ := hb.Bounds(date)
	if !e.Alert.Cancel && !e.Alert.ExpiresAt.IsZero() && e.Alert.ExpiresAt.Before(end) {
		end = e.Alert.ExpiresAt
	}
	id := "alert-notify-" + mailHash(e.ID+":"+date)
	var existing heartbeat.Task
	if err = alertLoad(ctx, tx, "job", id, &existing); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	job := heartbeat.Task{ID: id, Title: "上海官方天气预警", Prompt: renderAlert(e), At: s.now().Format(time.RFC3339), EndsAt: end.Format(time.RFC3339), Timezone: "Asia/Shanghai", Kind: "message", ApprovalMode: "read-only", Enabled: true, WorkspaceRoot: o.Workspace, Target: o.Target, CreatedAt: s.now().UnixMilli(), ModuleID: "weather_alert_notification", AlertEventID: e.ID}
	if err = job.Validate(); err != nil {
		return err
	}
	if err = saveEntity(ctx, tx, "job", id, job); err != nil {
		return err
	}
	e.JobID = id
	return saveEntity(ctx, tx, "alert_event", e.ID, e)
}
func (s AlertService) Admission(ctx context.Context, t heartbeat.Task) (bool, error) {
	allowed := false
	err := s.repo().transact(ctx, func(tx *sql.Tx) error {
		var event AlertEvent
		if e := alertLoad(ctx, tx, "alert_event", t.AlertEventID, &event); e != nil {
			return e
		}
		var record AlertRecord
		if e := alertLoad(ctx, tx, "alert_record", event.Key, &record); e != nil {
			return e
		}
		allowed = s.now().Sub(record.ObservedAt) <= 10*time.Minute && event.JobID == t.ID && record.EventID == event.ID && event.DeliveredAt.IsZero() && !event.Superseded && alertSame(record.Alert, event.Alert) && (event.Alert.Cancel || !alertExpired(record.Alert, s.now()))
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) || err == nil && !allowed {
		return false, heartbeat.ErrOccurrenceChanged
	}
	return allowed, err
}
func (s AlertService) Acknowledge(ctx context.Context, t heartbeat.Task, _ heartbeat.Run) error {
	if t.ModuleID != "weather_alert_notification" {
		return nil
	}
	return s.repo().transact(ctx, func(tx *sql.Tx) error {
		var e AlertEvent
		if err := alertLoad(ctx, tx, "alert_event", t.AlertEventID, &e); err != nil {
			return err
		}
		if e.JobID != t.ID {
			return errors.New("official alert notification ownership changed")
		}
		e.DeliveredAt = s.now()
		if err := saveEntity(ctx, tx, "alert_event", e.ID, e); err != nil {
			return err
		}
		var r AlertRecord
		if err := alertLoad(ctx, tx, "alert_record", e.Key, &r); err != nil {
			return err
		}
		r.LastDeliveredAt = s.now()
		return saveEntity(ctx, tx, "alert_record", e.Key, r)
	})
}
