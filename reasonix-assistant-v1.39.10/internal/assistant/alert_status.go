package assistant

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"reasonix/internal/heartbeat"
)

func (s AlertService) Status(ctx context.Context) (any, error) {
	c, err := s.Config(ctx)
	if err != nil {
		return nil, err
	}
	var records []AlertRecord
	var health []AlertHealth
	var events []AlertEvent
	issues := []map[string]string{}
	counts := map[string]int{"pending": 0, "delivered": 0, "superseded": 0}
	err = s.repo().transact(ctx, func(tx *sql.Tx) error {
		var e error
		if records, e = loadEntities[AlertRecord](ctx, tx, "alert_record"); e != nil {
			return e
		}
		if health, e = loadEntities[AlertHealth](ctx, tx, "alert_health"); e != nil {
			return e
		}
		if events, e = loadEntities[AlertEvent](ctx, tx, "alert_event"); e != nil {
			return e
		}
		for _, event := range events {
			state := "pending"
			if !event.DeliveredAt.IsZero() {
				state = "delivered"
			} else if event.Superseded {
				state = "superseded"
			}
			counts[state]++
			if state != "pending" || event.JobID == "" {
				continue
			}
			var job heartbeat.Task
			if e = alertLoad(ctx, tx, "job", event.JobID, &job); e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if len(job.History) > 0 {
				last := job.History[len(job.History)-1]
				if last.Status == "delivery_uncertain" || last.Status == "delivery_failed" || last.Status == "failed" {
					issues = append(issues, map[string]string{"event_id": event.ID, "job_id": event.JobID, "status": last.Status})
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	states := []map[string]any{}
	for _, r := range records {
		state := "observed_active"
		if r.Alert.Cancel {
			state = "explicitly_cancelled"
		} else if alertExpired(r.Alert, s.now()) {
			state = "expired"
		} else if s.now().Sub(r.ObservedAt) > 10*time.Minute {
			state = "not_reconfirmed"
		}
		states = append(states, map[string]any{"state": state, "record": r})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].ObservedAt.After(events[j].ObservedAt) })
	if len(events) > 30 {
		events = events[:30]
	}
	fresh := 0
	for _, h := range health {
		if h.Error == "" && !h.LastSuccess.IsZero() && s.now().Sub(h.LastSuccess) <= time.Duration(max(900, c.PollSeconds*2))*time.Second {
			fresh++
		}
	}
	coverage := "unavailable"
	if len(health) == 0 {
		coverage = "not_checked"
	} else if fresh == 3 {
		coverage = "healthy"
	} else if fresh > 0 {
		coverage = "degraded"
	}
	return map[string]any{"config": c, "coverage": coverage, "sources": health, "records": states, "event_counts": counts, "recent_events": events, "delivery_issues": issues, "queried_at": s.now(), "untrusted_external_data": true, "policy": "Any one credible current official source may trigger a new warning or upgrade; empty/failed sources cannot veto or cancel. Missing expiry is not an invented expiry. Retained records are not guaranteed current when not_reconfirmed. All automatic work obeys the routine. Native checks and messages make no model calls."}, nil
}
func (s AlertService) queueAlertHealth(ctx context.Context, tx *sql.Tx, o Options, date, message string) error {
	id := "alert-health-" + date
	var old heartbeat.Task
	if err := alertLoad(ctx, tx, "job", id, &old); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	hb, err := loadHeartbeatConfig(ctx, tx)
	if err != nil {
		return err
	}
	_, end, _ := hb.Bounds(date)
	job := heartbeat.Task{ID: id, Title: "天气预警来源异常", Prompt: message, At: s.now().Format(time.RFC3339), EndsAt: end.Format(time.RFC3339), Timezone: "Asia/Shanghai", Kind: "message", ApprovalMode: "read-only", Enabled: true, WorkspaceRoot: o.Workspace, Target: o.Target, CreatedAt: s.now().UnixMilli(), ModuleID: "weather_alert_health"}
	if err = job.Validate(); err != nil {
		return err
	}
	return saveEntity(ctx, tx, "job", id, job)
}
func (s AlertService) HealthAdmission(ctx context.Context) (bool, error) {
	allowed := false
	err := s.repo().transact(ctx, func(tx *sql.Tx) error {
		hs, e := loadEntities[AlertHealth](ctx, tx, "alert_health")
		if e != nil {
			return e
		}
		bad := 0
		for _, h := range hs {
			if h.Failures >= 3 {
				bad++
			}
		}
		allowed = bad == 3
		return nil
	})
	return allowed, err
}
