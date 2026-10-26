package assistant

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func githubDiscoverySeconds(c GitHubConfig) int {
	if c.DiscoverySeconds == 0 {
		return 300
	}
	return c.DiscoverySeconds
}

// Heartbeat offers work on minute boundaries. Align successful next checks to
// that clock so HTTP latency cannot make every following occurrence skip.
func githubNextCheck(now time.Time, seconds int) time.Time {
	return now.Truncate(time.Minute).Add(time.Duration(seconds) * time.Second)
}

func (s GitHubService) applyCadence(ctx context.Context, tx *sql.Tx, c *GitHubConfig, r GitHubRequest) error {
	old := c.PollSeconds
	if r.PollSeconds != nil {
		if *r.PollSeconds < 60 || *r.PollSeconds > 3600 {
			return errors.New("poll_seconds must be 60..3600")
		}
		c.PollSeconds = *r.PollSeconds
	}
	if r.DiscoverySeconds != nil {
		if *r.DiscoverySeconds < 300 || *r.DiscoverySeconds > 3600 {
			return errors.New("discovery_seconds must be 300..3600")
		}
		c.DiscoverySeconds = *r.DiscoverySeconds
		c.DiscoveryAt = s.now()
	}
	if c.DiscoverySeconds == 0 {
		c.DiscoverySeconds = 300
	}
	if c.PollSeconds >= old || old == 0 {
		return nil
	}
	watches, err := loadEntities[GitHubWatch](ctx, tx, "github_pr_watch")
	if err != nil {
		return err
	}
	for _, w := range watches {
		// Changing normal cadence cannot erase a failure/limit backoff.
		if !w.Enabled || w.Finished || w.Failures > 0 {
			continue
		}
		due := githubNextCheck(w.CheckedAt, c.PollSeconds)
		if due.Before(w.NextCheckAt) {
			w.NextCheckAt = due
			w.Revision++
			if err := saveEntity(ctx, tx, "github_pr_watch", w.ID, w); err != nil {
				return err
			}
		}
	}
	return nil
}
