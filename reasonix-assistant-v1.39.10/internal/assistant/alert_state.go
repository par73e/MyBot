package assistant

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s AlertService) ingest(ctx context.Context, a OfficialAlert, o Options, revision int) error {
	return s.repo().transact(ctx, func(tx *sql.Tx) error {
		enabled, err := s.alertCurrent(ctx, tx, revision)
		if err != nil || !enabled {
			return err
		}
		var old AlertRecord
		err = alertLoad(ctx, tx, "alert_record", a.Key, &old)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		kind := alertTransition(old, a, s.now())
		switch kind {
		case "ignore":
			return nil
		case "expiry":
			return s.mergeAlertExpiry(ctx, tx, old, a)
		case "enrich":
			return s.enrichAlert(ctx, tx, old, a)
		}
		// A later publication supersedes queued stale messages before any sender can
		// deliver them. Source lag/empty results never enter this function as clears.
		if old.EventID != "" {
			var event AlertEvent
			if e := alertLoad(ctx, tx, "alert_event", old.EventID, &event); e != nil {
				return e
			}
			if event.DeliveredAt.IsZero() {
				event.Superseded = true
				if e := saveEntity(ctx, tx, "alert_event", event.ID, event); e != nil {
					return e
				}
			}
		}
		record := AlertRecord{Alert: a, Revision: old.Revision + 1, ObservedAt: s.now(), LastDeliveredAt: old.LastDeliveredAt}
		id := "alert-event-" + mailHash(a.Key+":"+a.PublishedAt.UTC().Format(time.RFC3339Nano)+":"+a.Level+":"+kind)
		var prior AlertEvent
		e := alertLoad(ctx, tx, "alert_event", id, &prior)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e == nil {
			record.EventID = id
			return saveEntity(ctx, tx, "alert_record", a.Key, record)
		}
		event := AlertEvent{ID: id, Key: a.Key, Kind: kind, Alert: a, ObservedAt: s.now()}
		record.EventID = id
		if err = saveEntity(ctx, tx, "alert_event", id, event); err != nil {
			return err
		}
		if err = saveEntity(ctx, tx, "alert_record", a.Key, record); err != nil {
			return err
		}
		return s.queueAlert(ctx, tx, event, o)
	})
}
func alertTransition(old AlertRecord, a OfficialAlert, now time.Time) string {
	if !old.Alert.PublishedAt.IsZero() && a.PublishedAt.Before(old.Alert.PublishedAt) {
		return "ignore"
	}
	if old.Alert.Cancel && !a.Cancel && !a.PublishedAt.After(old.Alert.PublishedAt) {
		return "ignore"
	}
	if a.Cancel && (old.Alert.Key == "" || old.Alert.Cancel) {
		return "ignore"
	}
	if alertExpired(a, now) && !a.Cancel {
		return "expiry"
	}
	if old.Alert.Key != "" && !a.Cancel && !old.Alert.Cancel && a.PublishedAt.Equal(old.Alert.PublishedAt) && a.Rank <= old.Alert.Rank {
		if a.Rank == old.Alert.Rank {
			return "enrich"
		}
		return "ignore"
	}
	if a.Cancel {
		return "cancel"
	}
	if old.Alert.Key == "" || old.Alert.Cancel || alertExpired(old.Alert, now) {
		return "new"
	}
	if a.Rank > old.Alert.Rank {
		return "upgrade"
	}
	if a.Rank == old.Alert.Rank && a.Body != "" && a.Body == old.Alert.Body && a.PublishedAt.Sub(old.Alert.PublishedAt) <= 10*time.Minute {
		return "enrich"
	}
	return "update"
}
func (s AlertService) alertCurrent(ctx context.Context, tx *sql.Tx, revision int) (bool, error) {
	var c AlertConfig
	if err := alertLoad(ctx, tx, "alert_config", "default", &c); err != nil {
		return false, err
	}
	hb, err := loadHeartbeatConfig(ctx, tx)
	return err == nil && c.Enabled && c.Revision == revision && alertWindow(hb, s.now()), err
}
func (s AlertService) mergeAlertExpiry(ctx context.Context, tx *sql.Tx, old AlertRecord, a OfficialAlert) error {
	if old.Alert.Key != "" && old.Alert.PublishedAt.Equal(a.PublishedAt) && old.Alert.Rank == a.Rank {
		old.Alert.ExpiresAt = a.ExpiresAt
		old.Revision++
		return saveEntity(ctx, tx, "alert_record", a.Key, old)
	}
	return nil
}
func (s AlertService) enrichAlert(ctx context.Context, tx *sql.Tx, old AlertRecord, a OfficialAlert) error {
	old.ObservedAt = s.now()
	if old.Alert.Body == "" && a.Body != "" {
		old.Alert.Body = a.Body
		old.Alert.URL = a.URL
		old.Alert.Source = a.Source
	}
	if !a.ExpiresAt.IsZero() {
		old.Alert.ExpiresAt = a.ExpiresAt
	}
	old.Revision++
	return saveEntity(ctx, tx, "alert_record", a.Key, old)
}
func alertSame(a, b OfficialAlert) bool {
	return a.Key == b.Key && a.Rank == b.Rank && a.PublishedAt.Equal(b.PublishedAt) && a.Cancel == b.Cancel
}
func alertSourceName(v string) string {
	return map[string]string{"nmc": "中央气象台网站", "shanghai": "中国天气网上海频道", "weathercn": "中国天气网"}[v]
}
func renderAlert(e AlertEvent) string {
	a := e.Alert
	kind := map[string]string{"new": "新发布", "upgrade": "等级升级", "update": "更新", "cancel": "明确解除"}[e.Kind]
	lines := []string{addressPrefix() + "上海天气预警：", a.Area + a.Hazard + a.Level + "预警 · " + kind, "发布时间：" + a.PublishedAt.In(mustShanghai()).Format("01月02日 15:04")}
	if !a.ExpiresAt.IsZero() && !a.Cancel {
		lines = append(lines, "有效至："+a.ExpiresAt.In(mustShanghai()).Format("01月02日 15:04"))
	}
	if a.Body != "" {
		lines = append(lines, cleanAlertText(a.Body, 320))
	} else {
		lines = append(lines, "已取得官方预警列表，详情正文暂未取得。")
	}
	lines = append(lines, "来源："+alertSourceName(a.Source), "原文："+a.URL)
	return strings.Join(lines, "\n")
}
