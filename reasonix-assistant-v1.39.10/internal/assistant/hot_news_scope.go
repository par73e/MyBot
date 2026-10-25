package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reasonix/internal/heartbeat"
	"strconv"
)

type newsExecutionBinding struct {
	SnapshotID string `json:"snapshot_id"`
}

func newsBackgroundIdentity() (string, int64, error) {
	id := os.Getenv("REASONIX_ASSISTANT_JOB_ID")
	n, e := strconv.ParseInt(os.Getenv("REASONIX_ASSISTANT_NEWS_ATTEMPT"), 10, 64)
	if os.Getenv("REASONIX_ASSISTANT_NEWS_BACKGROUND") != "1" || id == "" || e != nil || n <= 0 {
		return "", 0, errors.New("news background identity missing")
	}
	return id, n, nil
}
func (s HotNewsService) Prepare(ctx context.Context, t heartbeat.Task) error {
	key := courseReadID(t.ID, t.RunStartedAt)
	snap, e := s.snapshot(ctx, "")
	if e != nil {
		return e
	}
	return NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		return saveEntity(ctx, tx, "ai_news_binding", key, newsExecutionBinding{SnapshotID: snap.ID})
	})
}
func (s HotNewsService) boundSnapshot(ctx context.Context, requested string) (HotNewsSnapshot, error) {
	if os.Getenv("REASONIX_ASSISTANT_NEWS_BACKGROUND") != "1" {
		return s.snapshot(ctx, requested)
	}
	id, n, e := newsBackgroundIdentity()
	if e != nil {
		return HotNewsSnapshot{}, e
	}
	var raw string
	var b newsExecutionBinding
	e = NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='ai_news_binding' AND id=?", courseReadID(id, n)).Scan(&raw)
	})
	if e != nil {
		return HotNewsSnapshot{}, e
	}
	if e = json.Unmarshal([]byte(raw), &b); e != nil {
		return HotNewsSnapshot{}, e
	}
	if requested != "" && requested != b.SnapshotID {
		return HotNewsSnapshot{}, errors.New("news snapshot is host-bound")
	}
	return s.loadSnapshot(ctx, b.SnapshotID)
}
func (s HotNewsService) markRead(ctx context.Context, kind string, ids []string) error {
	if os.Getenv("REASONIX_ASSISTANT_NEWS_BACKGROUND") != "1" {
		return nil
	}
	id, n, e := newsBackgroundIdentity()
	if e != nil {
		return e
	}
	return NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		key := courseReadID(id, n)
		if kind == "list" {
			return saveEntity(ctx, tx, "ai_news_list_receipt", key, true)
		}
		for _, item := range ids {
			if e := saveEntity(ctx, tx, "ai_news_read_receipt", key+":"+item, true); e != nil {
				return e
			}
		}
		return nil
	})
}
