package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"reasonix/internal/heartbeat"
)

func githubRead[T any](ctx context.Context, repo Repository, kind, id string) (T, error) {
	var v T
	err := repo.transact(ctx, func(tx *sql.Tx) error { return githubLoad(ctx, tx, kind, id, &v) })
	return v, err
}

func githubLoad(ctx context.Context, tx *sql.Tx, kind, id string, out any) error {
	var raw string
	if err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind=? AND id=?", kind, id).Scan(&raw); err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), out)
}

func (s GitHubService) httpState(ctx context.Context, path string) (githubHTTPState, time.Time, error) {
	var cache githubHTTPState
	var backoff githubBackoff
	err := s.repo().transact(ctx, func(tx *sql.Tx) error {
		for _, entry := range []struct {
			kind, id string
			out      any
		}{{"github_http_cache", mailHash(path), &cache}, {"github_backoff", "default", &backoff}} {
			if err := githubLoad(ctx, tx, entry.kind, entry.id, entry.out); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		return nil
	})
	return cache, backoff.Until, err
}

func (s GitHubService) saveHTTPState(ctx context.Context, path string, cache githubHTTPState) error {
	return s.repo().transact(ctx, func(tx *sql.Tx) error {
		return saveEntity(ctx, tx, "github_http_cache", mailHash(path), cache)
	})
}

func (s GitHubService) persistBackoff(ctx context.Context, b githubBackoff) error {
	return s.repo().transact(ctx, func(tx *sql.Tx) error {
		return saveEntity(ctx, tx, "github_backoff", "default", b)
	})
}

func (s GitHubService) Config(ctx context.Context) (GitHubConfig, error) {
	c, err := githubRead[GitHubConfig](ctx, s.repo(), "github_config", "default")
	if errors.Is(err, sql.ErrNoRows) {
		return GitHubConfig{PollSeconds: 60, DiscoverySeconds: 300}, nil
	}
	return c, err
}

func (s GitHubService) Watches(ctx context.Context) ([]GitHubWatch, error) {
	var watches []GitHubWatch
	err := s.repo().transact(ctx, func(tx *sql.Tx) error {
		var err error
		watches, err = loadEntities[GitHubWatch](ctx, tx, "github_pr_watch")
		return err
	})
	return watches, err
}

func (s GitHubService) status(ctx context.Context) (any, error) {
	cfg, err := s.Config(ctx)
	if err != nil {
		return nil, err
	}
	watches, err := s.Watches(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{"pending": 0, "delivered": 0, "superseded": 0}
	issues := []map[string]string{}
	err = s.repo().transact(ctx, func(tx *sql.Tx) error {
		events, err := loadEntities[GitHubEvent](ctx, tx, "github_pr_event")
		if err != nil {
			return err
		}
		for _, event := range events {
			switch {
			case !event.DeliveredAt.IsZero():
				counts["delivered"]++
			case event.Superseded:
				counts["superseded"]++
			default:
				counts["pending"]++
			}
		}
		jobs, err := loadEntities[heartbeat.Task](ctx, tx, "job")
		if err != nil {
			return err
		}
		for _, job := range jobs {
			if job.ModuleID != "github_pr_notification" || len(job.History) == 0 {
				continue
			}
			status := job.History[len(job.History)-1].Status
			switch status {
			case "delivery_failed", "delivery_uncertain", "delivery_expired":
				issues = append(issues, map[string]string{"watch_id": job.GitHubWatchID, "status": status})
			}
		}
		return nil
	})
	return map[string]any{"config": cfg, "watches": watches, "event_counts": counts, "delivery_issues": issues, "read_only_github": true}, err
}
