package assistant

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"regexp"
	"strings"
)

var githubLogin = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)

func (s GitHubService) Apply(ctx context.Context, r GitHubRequest) (any, error) {
	switch r.Action {
	case "status":
		return s.status(ctx)
	case "list":
		return s.Watches(ctx)
	case "query":
		if r.URL == "" {
			w, err := githubRead[GitHubWatch](ctx, s.repo(), "github_pr_watch", r.ID)
			if err != nil {
				return nil, err
			}
			r.URL = w.URL
		}
		return s.Snapshot(ctx, r.URL)
	}
	if os.Getenv("REASONIX_ASSISTANT_BACKGROUND") == "1" {
		return nil, errors.New("background data cannot configure GitHub tracking; ask Sir in the foreground")
	}
	switch r.Action {
	case "configure":
		return s.configure(ctx, r)
	case "add":
		return s.add(ctx, r.URL, false)
	case "remove":
		return s.remove(ctx, r)
	case "discover":
		return s.discover(ctx, true)
	default:
		return nil, errors.New("expected status, list, query, configure, add, remove or discover")
	}
}

func (s GitHubService) configure(ctx context.Context, r GitHubRequest) (GitHubConfig, error) {
	login, err := s.githubIdentity(ctx, r.Login)
	if err != nil {
		return GitHubConfig{}, err
	}
	r.Login = login
	if _, err := (HeartbeatService{Home: s.Home}).Ensure(ctx); err != nil {
		return GitHubConfig{}, err
	}
	var result GitHubConfig
	err = s.repo().transact(ctx, func(tx *sql.Tx) error {
		cfg := GitHubConfig{PollSeconds: 60, DiscoverySeconds: 300}
		if err := githubLoad(ctx, tx, "github_config", "default", &cfg); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if r.Revision != cfg.Revision {
			return errors.New("GitHub configuration revision changed; read status before configuring")
		}
		if cfg.Login != "" && !strings.EqualFold(cfg.Login, r.Login) {
			return errors.New("switching GitHub identity requires a separate explicit migration")
		}
		if err := s.applyCadence(ctx, tx, &cfg, r); err != nil {
			return err
		}
		cfg.Login = login
		if cfg.Revision == 0 {
			cfg.Enabled = true
		}
		if r.Enabled != nil {
			cfg.Enabled = *r.Enabled
		}
		if r.AutoDiscover != nil {
			cfg.AutoDiscover = *r.AutoDiscover
			cfg.DiscoveryAt = s.now()
		}
		cfg.Revision++
		if err := saveEntity(ctx, tx, "github_config", "default", cfg); err != nil {
			return err
		}
		hb, err := loadHeartbeatConfig(ctx, tx)
		if err != nil {
			return err
		}
		hb.ModuleEnabled["github_pr_check"] = cfg.Enabled
		tr, exists := hb.Triggers["github_prs"]
		if !exists {
			tr = ModuleTrigger{ID: "github_prs", Title: "GitHub PR进度检查", Mode: "interval", RoutineBound: true, Modules: []string{"github_pr_check"}, IntervalSeconds: 60}
		}
		tr.Enabled = cfg.Enabled
		hb.Triggers[tr.ID] = tr
		hb.Revision++
		if err := hb.Validate(); err != nil {
			return err
		}
		result = cfg
		return saveEntity(ctx, tx, "heartbeat_config", heartbeatConfigID, hb)
	})
	return result, err
}

func (s GitHubService) add(ctx context.Context, raw string, automatic bool) (GitHubWatch, error) {
	repo, n, err := parseGitHubPR(raw)
	if err != nil {
		return GitHubWatch{}, err
	}
	id := "gh-watch-" + mailHash(repo+":"+itoa(n))
	if existing, e := githubRead[GitHubWatch](ctx, s.repo(), "github_pr_watch", id); e == nil && (existing.Enabled && existing.Baseline != nil || automatic && existing.Excluded) {
		return existing, nil
	} else if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return GitHubWatch{}, e
	}
	snap, err := s.Snapshot(ctx, raw)
	if err != nil {
		return GitHubWatch{}, err
	}
	w := GitHubWatch{ID: id, Revision: 1, URL: snap.URL, Repository: snap.Repository, Number: n, Enabled: true, Automatic: automatic, Baseline: &snap, CheckedAt: s.now()}
	updateGitHubCI(&w, snap)
	err = s.repo().transact(ctx, func(tx *sql.Tx) error {
		var old GitHubWatch
		if e := githubLoad(ctx, tx, "github_pr_watch", id, &old); e == nil {
			if old.Enabled && old.Baseline != nil || automatic && old.Excluded {
				w = old
				return nil
			}
			w.Revision = old.Revision + 1
			pending, e := loadEntities[GitHubEvent](ctx, tx, "github_pr_event")
			if e != nil {
				return e
			}
			for _, event := range pending {
				if event.WatchID == old.ID && event.DeliveredAt.IsZero() {
					event.Superseded = true
					if e := saveEntity(ctx, tx, "github_pr_event", event.ID, event); e != nil {
						return e
					}
				}
			}
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		return saveEntity(ctx, tx, "github_pr_watch", id, w)
	})
	return w, err
}

func (s GitHubService) remove(ctx context.Context, r GitHubRequest) (GitHubWatch, error) {
	var w GitHubWatch
	err := s.repo().transact(ctx, func(tx *sql.Tx) error {
		if e := githubLoad(ctx, tx, "github_pr_watch", r.ID, &w); e != nil {
			return e
		}
		if w.Revision != r.Revision {
			return errors.New("watch revision changed; read list before removing")
		}
		w.Enabled = false
		w.Excluded = true
		w.Revision++
		return saveEntity(ctx, tx, "github_pr_watch", w.ID, w)
	})
	return w, err
}

func updateGitHubCI(w *GitHubWatch, snap GitHubSnapshot) {
	if w.FailedCIHead != snap.HeadSHA || snap.CI.State == "success" {
		w.FailedCIHead = ""
		w.FailedNames = nil
	}
	if snap.CI.State == "failure" {
		w.FailedCIHead = snap.HeadSHA
		w.FailedNames = append([]string(nil), snap.CI.Failed...)
	}
	w.Finished = snap.State != "open"
}

func (s GitHubService) githubIdentity(ctx context.Context, requested string) (string, error) {
	known, err := s.Config(ctx)
	if err != nil {
		return "", err
	}
	identity := githubUser{Login: known.Login}
	if known.Revision == 0 {
		if _, err := s.get(ctx, "/user", &identity); err != nil {
			return "", err
		}
	}
	if requested == "" {
		requested = known.Login
	}
	if !githubLogin.MatchString(requested) || !strings.EqualFold(requested, identity.Login) {
		return "", errors.New("auto-discovery login must match the configured credential account")
	}
	return identity.Login, nil
}
