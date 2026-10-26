package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"reasonix/internal/heartbeat"
)

func TestGitHubCIFailureRecoveryAndNewHeadGuards(t *testing.T) {
	hs, s, f := githubFixtureService(t)
	configureGitHubFixture(t, s, false)
	if _, err := s.add(t.Context(), "https://github.com/org/project/pull/1", false); err != nil {
		t.Fatal(err)
	}
	poll := func() {
		t.Helper()
		f.now = f.now.Add(11 * time.Minute)
		if err := s.Poll(t.Context(), hs.Options); err != nil {
			t.Fatal(err)
		}
	}
	f.check = "failure"
	poll()
	events := githubEventsForTest(t, s)
	if len(events) != 1 || events[0].State != "failure" {
		t.Fatal(events)
	}
	// Permission loss is unknown, not green. Recovering the same failed view
	// must not send a second failure notification.
	f.failPath = "/check-runs"
	f.failCode = 404
	poll()
	watches, _ := s.Watches(t.Context())
	if watches[0].Baseline.CI.State != "unknown" {
		t.Fatal("unknown CI called passed")
	}
	f.failPath = ""
	poll()
	if len(githubEventsForTest(t, s)) != 1 {
		t.Fatal("unknown->failure duplicated CI alert")
	}
	f.check = "pending"
	poll()
	f.check = "success"
	poll()
	events = githubEventsForTest(t, s)
	if len(events) != 2 || events[1].State != "success" {
		t.Fatal("failure->pending->success lost recovery", events)
	}
	// Old pending failure is no longer deliverable after a newer head is observed.
	f.check = "failure"
	poll()
	events = githubEventsForTest(t, s)
	var oldJob heartbeat.Task
	jobs, _ := (JobStore{Repository: s.repo()}).List(t.Context())
	for _, j := range jobs {
		if j.ID == events[len(events)-1].JobID {
			oldJob = j
		}
	}
	f.sha = strings.Repeat("b", 40)
	f.check = "pending"
	poll()
	if ok, err := s.Admission(t.Context(), oldJob); ok || !errors.Is(err, heartbeat.ErrOccurrenceChanged) {
		t.Fatal("old head failure can still deliver", ok, err)
	}
}

func TestGitHubReviewsInlineAndMerged(t *testing.T) {
	hs, s, f := githubFixtureService(t)
	configureGitHubFixture(t, s, false)
	if _, err := s.add(t.Context(), "https://github.com/org/project/pull/1", false); err != nil {
		t.Fatal(err)
	}
	one := githubCommentFixture(99, "reviewer", "old approval")
	one["state"] = "APPROVED"
	one["submitted_at"] = "2026-10-27T02:00:00Z"
	one["commit_id"] = f.sha
	two := githubCommentFixture(100, "reviewer", "please fix edge case")
	two["state"] = "CHANGES_REQUESTED"
	two["submitted_at"] = "2026-10-27T02:00:00Z"
	two["commit_id"] = f.sha
	f.reviews = []map[string]any{one, two}
	f.inline = []map[string]any{githubCommentFixture(1, "reviewer", "inline boundary comment")}
	f.now = f.now.Add(11 * time.Minute)
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	events := githubEventsForTest(t, s)
	if len(events) != 2 {
		t.Fatal(events)
	}
	for _, e := range events {
		if e.State == "APPROVED" {
			t.Fatal("stale approval notified instead of latest review")
		}
	}
	f.state = "merged"
	f.now = f.now.Add(11 * time.Minute)
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	events = githubEventsForTest(t, s)
	if len(events) != 3 || events[2].State != "merged" {
		t.Fatal(events)
	}
	watches, _ := s.Watches(t.Context())
	if !watches[0].Finished {
		t.Fatal("merged PR continues routine polling")
	}
}

type githubFailingSender struct {
	count int
	err   error
}

func (s *githubFailingSender) Send(context.Context, heartbeat.Target, string) error {
	s.count++
	return s.err
}

func TestGitHubDeliveryFailureKeepsEventsAndDoesNotRefetch(t *testing.T) {
	hs, s, f := githubFixtureService(t)
	configureGitHubFixture(t, s, false)
	if _, err := s.add(t.Context(), "https://github.com/org/project/pull/1", false); err != nil {
		t.Fatal(err)
	}
	f.comments = []map[string]any{githubCommentFixture(1, "reviewer", "new reply")}
	f.now = f.now.Add(11 * time.Minute)
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	sender := &githubFailingSender{err: errors.New("mock transport failure")}
	modelCalls := 0
	runner := githubSummaryTestRunner(t, s)
	e := heartbeat.Engine{Store: JobStore{Repository: s.repo()}, Gate: hs.Allows, Sender: sender, Clock: s.Clock, AfterDelivery: s.Acknowledge, Runner: mailTestRunner(func(ctx context.Context, job heartbeat.Task) (heartbeat.Execution, error) {
		modelCalls++
		return runner.Run(ctx, job)
	})}
	if err := e.Tick(t.Context(), f.now); err == nil {
		t.Fatal("delivery error swallowed")
	}
	events := githubEventsForTest(t, s)
	if !events[0].DeliveredAt.IsZero() {
		t.Fatal("marked delivered before acknowledgement")
	}
	requests := f.requests
	sender.err = nil
	f.now = f.now.Add(11 * time.Second)
	if err := e.Tick(t.Context(), f.now); err != nil || sender.count != 2 || f.requests != requests || modelCalls != 1 {
		t.Fatal("delivery retry reran GitHub collection", err, sender.count)
	}
	events = githubEventsForTest(t, s)
	if events[0].DeliveredAt.IsZero() {
		t.Fatal("successful retry not acknowledged")
	}
}

func TestGitHubCIPermissionFailureDoesNotBlockHumanReplies(t *testing.T) {
	hs, s, f := githubFixtureService(t)
	configureGitHubFixture(t, s, false)
	if _, err := s.add(t.Context(), "https://github.com/org/project/pull/1", false); err != nil {
		t.Fatal(err)
	}
	f.comments = []map[string]any{githubCommentFixture(1, "reviewer", "please confirm this change")}
	f.failPath = "/check-runs"
	f.failCode = 403
	f.now = f.now.Add(11 * time.Minute)
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal("CI-only permission issue blocked PR discussion", err)
	}
	watches, _ := s.Watches(t.Context())
	if watches[0].Baseline.CI.State != "unknown" || watches[0].Baseline.CI.Error == "" {
		t.Fatal("CI permission issue was hidden")
	}
	events := githubEventsForTest(t, s)
	if len(events) != 1 || events[0].Kind != "comment" {
		t.Fatal("human reply lost", events)
	}
}
