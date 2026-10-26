package assistant

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"sort"
	"time"

	"reasonix/internal/identitylock"
)

func (s GitHubService) Poll(ctx context.Context, o Options) error {
	ctx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	unlock, err := identitylock.Acquire(ctx, s.repo().Path+".github-poll")
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := s.Config(ctx)
	if err != nil || !cfg.Enabled {
		return err
	}
	hb, err := (HeartbeatService{Home: s.Home}).Config(ctx)
	if err != nil {
		return err
	}
	if !githubWindow(hb, s.now()) {
		return nil
	}
	failures := []error{}
	if _, err = s.discover(ctx, false); err != nil {
		failures = append(failures, err)
		if e := s.recordDiscoveryError(ctx, err); e != nil {
			return e
		}
	}
	watches, err := s.Watches(ctx)
	if err != nil {
		return err
	}
	sort.SliceStable(watches, func(i, j int) bool { return watches[i].NextCheckAt.Before(watches[j].NextCheckAt) })
	checked := 0
	for _, w := range watches {
		if !w.Enabled || w.Excluded || w.Finished || s.now().Before(w.NextCheckAt) {
			continue
		}
		if checked >= 5 || ctx.Err() != nil {
			break
		}
		checked++
		snap, e := s.Snapshot(ctx, w.URL)
		if e = s.commitWatch(ctx, w, cfg, snap, e); e != nil {
			failures = append(failures, e)
		}
	}
	if err = s.queueNotifications(ctx, o, hb); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func (s GitHubService) recordDiscoveryError(ctx context.Context, cause error) error {
	return s.repo().transact(ctx, func(tx *sql.Tx) error {
		var cfg GitHubConfig
		if err := githubLoad(ctx, tx, "github_config", "default", &cfg); err != nil {
			return err
		}
		cfg.DiscoveryErr = cause.Error()
		cfg.DiscoveryAt = s.now().Add(5 * time.Minute)
		return saveEntity(ctx, tx, "github_config", "default", cfg)
	})
}

func (s GitHubService) commitWatch(ctx context.Context, w GitHubWatch, cfg GitHubConfig, snap GitHubSnapshot, readErr error) error {
	err := s.repo().transact(ctx, func(tx *sql.Tx) error {
		var active GitHubConfig
		if err := githubLoad(ctx, tx, "github_config", "default", &active); err != nil {
			return err
		}
		if !active.Enabled || active.Revision != cfg.Revision {
			return nil
		}
		var live GitHubWatch
		if e := githubLoad(ctx, tx, "github_pr_watch", w.ID, &live); e != nil {
			return e
		}
		if !live.Enabled || live.Revision != w.Revision {
			return nil
		}
		live.CheckedAt = s.now()
		live.NextCheckAt = githubNextCheck(s.now(), cfg.PollSeconds)
		if readErr != nil {
			live.Failures++
			live.Error = readErr.Error()
			live.NextCheckAt = s.now().Add(time.Duration(min(30, 1<<min(live.Failures, 5))) * time.Minute)
		} else {
			for _, event := range s.events(live, snap, cfg.Login) {
				if e := saveEntity(ctx, tx, "github_pr_event", event.ID, event); e != nil {
					return e
				}
			}
			live.Baseline = &snap
			live.URL = snap.URL
			live.Repository = snap.Repository
			live.Error = ""
			live.Failures = 0
			updateGitHubCI(&live, snap)
		}
		live.Revision++
		return saveEntity(ctx, tx, "github_pr_watch", live.ID, live)
	})
	if err != nil {
		return err
	}
	return readErr
}

func githubWindow(c HeartbeatConfig, now time.Time) bool {
	date := now.In(mustShanghai()).Format("2006-01-02")
	tr, ok := c.Triggers["github_prs"]
	if !ok || !tr.Enabled || !c.ModuleEnabled["github_pr_check"] || !slices.Contains(c.Selected(tr, date), "github_pr_check") {
		return false
	}
	start, end, open := c.TriggerBounds(tr, date)
	return open && !now.Before(start) && now.Before(end)
}
