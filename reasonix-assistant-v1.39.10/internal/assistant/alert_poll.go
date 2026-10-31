package assistant

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"reasonix/internal/identitylock"
)

func (s AlertService) Poll(ctx context.Context, o Options) error {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	if err := os.MkdirAll(filepath.Dir(s.repo().Path), 0o700); err != nil {
		return err
	}
	unlock, err := identitylock.Acquire(ctx, s.repo().Path+".alerts-poll")
	if err != nil {
		return err
	}
	defer unlock()
	c, err := s.Config(ctx)
	if err != nil || !c.Enabled {
		return err
	}
	hb, err := (HeartbeatService{Home: s.Home}).Config(ctx)
	if err != nil || !alertWindow(hb, s.now()) {
		return err
	}
	type source struct {
		id   string
		read func(context.Context, alertEmit) (alertSourceResult, error)
	}
	sources := []source{{"nmc", s.readNMC}, {"shanghai", s.readShanghai}, {"weathercn", s.readProduct}}
	workCtx, stopWork := context.WithTimeout(ctx, 30*time.Second)
	defer stopWork()
	failures := make(chan error, len(sources))
	var wg sync.WaitGroup
	for _, source := range sources {
		health, e := alertRead[AlertHealth](ctx, s.repo(), "alert_health", source.id)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			failures <- e
			continue
		}
		if health.Failures == 0 && !health.LastSuccess.IsZero() {
			aligned := nextAlertSlot(hb, health.LastSuccess, c.PollSeconds)
			if aligned.Before(health.NextAttempt) {
				health.NextAttempt = aligned
			}
		}
		if s.now().Before(health.NextAttempt) {
			continue
		}
		wg.Go(func() {
			stats, e := source.read(workCtx, func(a OfficialAlert) error { return s.ingest(ctx, a, o, c.Revision) })
			if saved := s.recordAlertHealth(ctx, source.id, stats, e, c.PollSeconds, hb); saved != nil {
				failures <- saved
			}
			// Per-source faults are observable degradation, not a failed whole job that
			// reruns healthy sources or withholds already queued alerts.
		})
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		err = errors.Join(err, e)
	}
	if ctx.Err() != nil {
		return errors.Join(err, errors.New("official alert round exceeded its time budget; completed observations retained"))
	}
	if err != nil {
		return err
	}
	return s.reconcileAlerts(ctx, o, c.Revision)
}
func (s AlertService) recordAlertHealth(ctx context.Context, id string, stats alertSourceResult, cause error, seconds int, hb HeartbeatConfig) error {
	return s.repo().transact(ctx, func(tx *sql.Tx) error {
		var h AlertHealth
		if e := alertLoad(ctx, tx, "alert_health", id, &h); e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		h.Source = id
		h.CheckedAt = s.now()
		h.Count = stats.Count
		h.DetailErrors = stats.DetailErrors
		if cause == nil {
			h.Error = ""
			h.Failures = 0
			h.LastSuccess = s.now()
			h.NextAttempt = nextAlertSlot(hb, s.now(), seconds)
		} else {
			h.Failures++
			h.Error = cause.Error()
			h.NextAttempt = s.now().Add(time.Duration(min(60, 5*(1<<min(h.Failures-1, 4)))) * time.Minute)
		}
		return saveEntity(ctx, tx, "alert_health", id, h)
	})
}
func (s AlertService) reconcileAlerts(ctx context.Context, o Options, revision int) error {
	return s.repo().transact(ctx, func(tx *sql.Tx) error {
		current, err := s.alertCurrent(ctx, tx, revision)
		if err != nil || !current {
			return err
		}
		records, err := loadEntities[AlertRecord](ctx, tx, "alert_record")
		if err != nil {
			return err
		}
		for _, r := range records {
			if r.EventID == "" || s.now().Sub(r.ObservedAt) > 10*time.Minute {
				continue
			}
			var event AlertEvent
			if e := alertLoad(ctx, tx, "alert_event", r.EventID, &event); e != nil {
				return e
			}
			if err = s.queueAlert(ctx, tx, event, o); err != nil {
				return err
			}
		}
		return s.alertCoverageNotice(ctx, tx, o)
	})
}
func (s AlertService) alertCoverageNotice(ctx context.Context, tx *sql.Tx, o Options) error {
	if o.Target == nil {
		return nil
	}
	hs, err := loadEntities[AlertHealth](ctx, tx, "alert_health")
	if err != nil {
		return err
	}
	bad := 0
	for _, h := range hs {
		if h.Failures >= 3 {
			bad++
		}
	}
	if bad < 3 {
		return nil
	}
	// One factual outage notice per civil day; the ordinary outbox controls retries.
	date := s.now().In(mustShanghai()).Format("2006-01-02")
	return s.queueAlertHealth(ctx, tx, o, date, addressPrefix()+"上海天气预警的三个官方网页数据源均连续检查失败，暂时无法确认最新预警；此前记录仍保留。可通过上海天气发布或官方预警页核实。\nhttps://sh.weather.com.cn/zhyj/")
}

// Align a successful source to the same routine-relative slots used by its
// native trigger. A mid-slot restart must not skip the next scheduled check.
func nextAlertSlot(hb HeartbeatConfig, now time.Time, seconds int) time.Time {
	date := now.In(mustShanghai()).Format("2006-01-02")
	start, _, ok := hb.TriggerBounds(hb.Triggers["weather_alerts"], date)
	if !ok || now.Before(start) {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	period := time.Duration(seconds) * time.Second
	return start.Add((now.Sub(start)/period + 1) * period)
}
