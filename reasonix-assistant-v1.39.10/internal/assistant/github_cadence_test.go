package assistant

import (
	"testing"
	"time"
)

func TestGitHubFiveMinuteDiscoveryAndOneMinuteUpdates(t *testing.T) {
	hs, s, f := githubFixtureService(t)
	cfg := configureGitHubFixture(t, s, true)
	if cfg.PollSeconds != 60 || cfg.DiscoverySeconds != 300 {
		t.Fatal(cfg)
	}
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	requests := f.requests
	f.discovery = true
	f.now = f.now.Add(299 * time.Second)
	if err := s.Poll(t.Context(), hs.Options); err != nil || f.requests != requests {
		t.Fatal("discovery ran before five minutes", err)
	}
	f.now = f.now.Add(time.Second)
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	watches, _ := s.Watches(t.Context())
	if len(watches) != 1 || watches[0].Baseline == nil {
		t.Fatal("five-minute discovery missed new PR")
	}
	requests = f.requests
	f.comments = append(f.comments, githubCommentFixture(1, "reviewer", "new one-minute reply"))
	f.now = f.now.Add(59 * time.Second)
	if err := s.Poll(t.Context(), hs.Options); err != nil || f.requests != requests {
		t.Fatal("PR checked before one minute", err)
	}
	f.now = f.now.Add(time.Second)
	if err := s.Poll(t.Context(), hs.Options); err != nil || len(githubEventsForTest(t, s)) != 1 {
		t.Fatal("one-minute PR update was missed", err)
	}
}

func TestGitHubCadenceChangeReschedulesPendingWatch(t *testing.T) {
	hs, s, f := githubFixtureService(t)
	cfg := configureGitHubFixture(t, s, false)
	slower := 600
	cfg, err := s.configure(t.Context(), GitHubRequest{Revision: cfg.Revision, PollSeconds: &slower})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.add(t.Context(), "https://github.com/org/project/pull/1", false); err != nil {
		t.Fatal(err)
	}
	if err = s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	faster := 60
	if _, err = s.configure(t.Context(), GitHubRequest{Revision: cfg.Revision, PollSeconds: &faster}); err != nil {
		t.Fatal(err)
	}
	watches, _ := s.Watches(t.Context())
	if !watches[0].NextCheckAt.Equal(f.now.Add(time.Minute)) {
		t.Fatal("old ten-minute due time retained", watches[0].NextCheckAt)
	}
	bad := 1
	cfg, _ = s.Config(t.Context())
	if _, err = s.configure(t.Context(), GitHubRequest{Revision: cfg.Revision, PollSeconds: &bad}); err == nil {
		t.Fatal("unbounded one-second polling accepted")
	}
}

func TestGitHubMinuteSchedulerDoesNotSkipAfterRequestLatency(t *testing.T) {
	hs, s, f := githubFixtureService(t)
	configureGitHubFixture(t, s, true)
	start := f.now.Truncate(time.Minute)
	f.now = start.Add(20 * time.Second)
	f.discovery = true
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	cfg, _ := s.Config(t.Context())
	if !cfg.DiscoveryAt.Equal(start.Add(5 * time.Minute)) {
		t.Fatal("discovery missed fifth minute boundary", cfg.DiscoveryAt)
	}
	f.comments = append(f.comments, githubCommentFixture(1, "reviewer", "next minute reply"))
	f.now = start.Add(time.Minute + time.Second)
	if err := s.Poll(t.Context(), hs.Options); err != nil || len(githubEventsForTest(t, s)) != 1 {
		t.Fatal("request latency skipped the following minute", err)
	}
	requests := f.requests
	f.discovery = false
	f.now = start.Add(5*time.Minute + time.Second)
	if _, err := s.discover(t.Context(), false); err != nil || f.requests <= requests {
		t.Fatal("discovery skipped the fifth minute", err)
	}
}
