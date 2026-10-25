package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reasonix/internal/identitylock"
	"time"
)

func (s HotNewsService) loadSnapshot(ctx context.Context, id string) (HotNewsSnapshot, error) {
	var v HotNewsSnapshot
	var raw string
	e := NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='ai_news_snapshot' AND id=?", id).Scan(&raw)
	})
	if e != nil {
		return v, e
	}
	return v, json.Unmarshal([]byte(raw), &v)
}
func (s HotNewsService) snapshot(ctx context.Context, id string) (HotNewsSnapshot, error) {
	if id != "" {
		return s.loadSnapshot(ctx, id)
	}
	repo := NewRepository(s.Home)
	// Serialize collectors without holding the SQLite state transaction during HTTP.
	if e := repo.transact(ctx, func(*sql.Tx) error { return nil }); e != nil {
		return HotNewsSnapshot{}, e
	}
	release, e := identitylock.Acquire(ctx, repo.Path+".ai-news")
	if e != nil {
		return HotNewsSnapshot{}, e
	}
	defer release()
	var cache struct {
		SnapshotID string    `json:"snapshot_id"`
		At         time.Time `json:"at"`
	}
	var raw string
	e = repo.transact(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='ai_news_cache' AND id='v1' ").Scan(&raw)
	})
	if e == nil {
		if e = json.Unmarshal([]byte(raw), &cache); e != nil {
			return HotNewsSnapshot{}, e
		}
		if s.now().Sub(cache.At) >= 0 && s.now().Sub(cache.At) < 15*time.Minute {
			v, e := s.loadSnapshot(ctx, cache.SnapshotID)
			if e == nil && v.Status != "unavailable" {
				return v, nil
			}
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return HotNewsSnapshot{}, e
	}
	snap := s.collect(ctx, s.now())
	key, e := newID()
	if e != nil {
		return snap, e
	}
	snap.ID = "news-" + key
	e = repo.transact(ctx, func(tx *sql.Tx) error {
		if e := saveEntity(ctx, tx, "ai_news_snapshot", snap.ID, snap); e != nil {
			return e
		}
		return saveEntity(ctx, tx, "ai_news_cache", "v1", map[string]any{"snapshot_id": snap.ID, "at": snap.AsOf})
	})
	return snap, e
}
func (s HotNewsService) storeSnapshot(ctx context.Context, snap HotNewsSnapshot) error {
	return NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error { return saveEntity(ctx, tx, "ai_news_snapshot", snap.ID, snap) })
}
