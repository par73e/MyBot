package assistant

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"reasonix/internal/heartbeat"
)

func githubEventCurrent(e GitHubEvent, snap GitHubSnapshot) bool {
	if e.Kind == "ci" {
		return e.HeadSHA == snap.HeadSHA && e.State == snap.CI.State
	}
	if e.Kind == "state" {
		return e.State == snap.State
	}
	v, ok := snap.Items[e.SourceID]
	if !ok || v.Fingerprint != e.Fingerprint {
		return false
	}
	if e.Kind == "review" {
		for _, item := range snap.Items {
			if item.Kind == "review" && strings.EqualFold(item.Author, v.Author) && item.State != "PENDING" && (item.UpdatedAt > v.UpdatedAt || item.UpdatedAt == v.UpdatedAt && item.Sequence > v.Sequence) {
				return false
			}
		}
	}
	return true
}

func (s GitHubService) queueNotifications(ctx context.Context, o Options, hb HeartbeatConfig) error {
	if o.Target == nil {
		return nil
	}
	date := s.now().In(mustShanghai()).Format("2006-01-02")
	start, end, open := hb.Bounds(date)
	if !open || s.now().Before(start) || !s.now().Before(end) {
		return nil
	}
	return s.repo().transact(ctx, func(tx *sql.Tx) error {
		current, err := loadHeartbeatConfig(ctx, tx)
		if err != nil {
			return err
		}
		if !githubWindow(current, s.now()) {
			return nil
		}
		var cfg GitHubConfig
		if err := githubLoad(ctx, tx, "github_config", "default", &cfg); err != nil {
			return err
		}
		if !cfg.Enabled {
			return nil
		}
		watches, err := loadEntities[GitHubWatch](ctx, tx, "github_pr_watch")
		if err != nil {
			return err
		}
		events, err := loadEntities[GitHubEvent](ctx, tx, "github_pr_event")
		if err != nil {
			return err
		}
		for _, w := range watches {
			if !w.Enabled || w.Baseline == nil || w.Excluded {
				continue
			}
			pending := []GitHubEvent{}
			for _, e := range events {
				available, err := s.githubEventAvailable(ctx, tx, e, w)
				if err != nil {
					return err
				}
				if !available {
					continue
				}
				pending = append(pending, e)
			}
			if len(pending) == 0 {
				continue
			}
			sort.SliceStable(pending, func(i, j int) bool {
				if a, b := githubEventPriority(pending[i]), githubEventPriority(pending[j]); a != b {
					return a < b
				}
				return pending[i].ID < pending[j].ID
			})
			pending = pending[:min(5, len(pending))]
			ids := []string{}
			for _, e := range pending {
				ids = append(ids, e.ID)
			}
			id := "github-notify-" + mailHash(w.ID+":"+date+":"+strings.Join(ids, ","))
			var existing heartbeat.Task
			if err := githubLoad(ctx, tx, "job", id, &existing); err == nil {
				continue
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			job := heartbeat.Task{ID: id, Title: "GitHub PR进展", Prompt: renderGitHub(*w.Baseline, pending), At: s.now().Format(time.RFC3339), EndsAt: end.Format(time.RFC3339), Timezone: "Asia/Shanghai", Kind: "message", ApprovalMode: "read-only", Enabled: true, WorkspaceRoot: o.Workspace, Target: o.Target, CreatedAt: s.now().UnixMilli(), ModuleID: "github_pr_notification", GitHubWatchID: w.ID, GitHubEventIDs: ids}
			for _, event := range pending {
				if githubDiscussion(event.Kind) {
					job.Kind, job.Capability, job.Prompt = "agent", "github", GitHubSummaryInstructions
					job.DecideNotification, job.NewConversationEachRun, job.Model = true, true, o.Model
					break
				}
			}
			if err := job.Validate(); err != nil {
				return err
			}
			if err := saveEntity(ctx, tx, "job", id, job); err != nil {
				return err
			}
			for _, e := range pending {
				e.JobID = id
				if err := saveEntity(ctx, tx, "github_pr_event", e.ID, e); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func githubEventPriority(e GitHubEvent) int {
	if e.Kind == "state" {
		return 0
	}
	if e.Kind == "ci" || e.State == "CHANGES_REQUESTED" {
		return 1
	}
	if e.Kind == "review" {
		return 2
	}
	if e.Kind == "inline" {
		return 3
	}
	return 4
}

func (s GitHubService) Admission(ctx context.Context, t heartbeat.Task) (bool, error) {
	allowed := false
	err := s.repo().transact(ctx, func(tx *sql.Tx) error {
		var cfg GitHubConfig
		if e := githubLoad(ctx, tx, "github_config", "default", &cfg); e != nil {
			return e
		}
		var w GitHubWatch
		if e := githubLoad(ctx, tx, "github_pr_watch", t.GitHubWatchID, &w); e != nil {
			return e
		}
		if !cfg.Enabled || !w.Enabled || w.Excluded || w.Baseline == nil {
			return nil
		}
		if len(t.GitHubEventIDs) == 0 {
			return nil
		}
		for _, id := range t.GitHubEventIDs {
			var event GitHubEvent
			if e := githubLoad(ctx, tx, "github_pr_event", id, &event); e != nil {
				return e
			}
			if event.JobID != t.ID || event.WatchID != w.ID || !event.DeliveredAt.IsZero() || event.Superseded || !githubEventCurrent(event, *w.Baseline) {
				return nil
			}
		}
		allowed = true
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) || err == nil && !allowed {
		return false, heartbeat.ErrOccurrenceChanged
	}
	return allowed, err
}

func (s GitHubService) Acknowledge(ctx context.Context, t heartbeat.Task, _ heartbeat.Run) error {
	if t.ModuleID != "github_pr_notification" {
		return nil
	}
	return s.repo().transact(ctx, func(tx *sql.Tx) error {
		for _, id := range t.GitHubEventIDs {
			var event GitHubEvent
			if err := githubLoad(ctx, tx, "github_pr_event", id, &event); err != nil {
				return err
			}
			if event.JobID != t.ID {
				return errors.New("GitHub notification ownership changed")
			}
			event.DeliveredAt = s.now()
			if err := saveEntity(ctx, tx, "github_pr_event", id, event); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s GitHubService) githubEventAvailable(ctx context.Context, tx *sql.Tx, e GitHubEvent, w GitHubWatch) (bool, error) {
	if e.WatchID != w.ID || !e.DeliveredAt.IsZero() || e.Superseded {
		return false, nil
	}
	if e.Kind == "ci" && w.Baseline.CI.State == "unknown" && e.HeadSHA == w.Baseline.HeadSHA {
		return false, nil
	}
	if !githubEventCurrent(e, *w.Baseline) {
		e.Superseded = true
		return false, saveEntity(ctx, tx, "github_pr_event", e.ID, e)
	}
	if e.JobID == "" {
		return true, nil
	}
	var old heartbeat.Task
	if err := githubLoad(ctx, tx, "job", e.JobID, &old); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if old.Enabled && !old.Expired(s.now()) {
		return false, nil
	}
	if len(old.History) > 0 {
		switch old.History[len(old.History)-1].Status {
		case "delivered", "delivery_uncertain", "delivery_failed":
			return false, nil
		}
	}
	return true, nil
}
