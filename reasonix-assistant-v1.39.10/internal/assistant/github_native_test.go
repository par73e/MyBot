package assistant

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"reasonix/internal/heartbeat"
)

func TestGitHubNativeHeartbeatLaneDoesNotRunModel(t *testing.T) {
	hs, s, f := githubFixtureService(t)
	configureGitHubFixture(t, s, false)
	if _, err := s.add(t.Context(), "https://github.com/org/project/pull/1", false); err != nil {
		t.Fatal(err)
	}
	f.check = "failure"
	f.now = f.now.Add(11 * time.Minute)
	native, sent := 0, 0
	e := heartbeat.Engine{Store: JobStore{Repository: s.repo()}, Prepare: hs.Prepare, Gate: hs.Allows, Clock: s.Clock, Sender: courseSender(func() { sent++ }), AfterDelivery: s.Acknowledge,
		Runner: mailTestRunner(func(context.Context, heartbeat.Task) (heartbeat.Execution, error) {
			t.Fatal("PR checking/notification invoked a model")
			return heartbeat.Execution{}, nil
		}),
		Checker: mailTestRunner(func(ctx context.Context, task heartbeat.Task) (heartbeat.Execution, error) {
			if task.Kind == "mail_check" {
				return heartbeat.Execution{}, nil
			}
			if task.Kind != "module_check" || task.ModuleID != "github_pr_check" {
				t.Fatal("wrong native checker route", task.Kind, task.ModuleID)
			}
			native++
			return heartbeat.Execution{}, s.Poll(ctx, hs.Options)
		}),
	}
	if err := e.Tick(t.Context(), f.now); err != nil {
		t.Fatal(err)
	}
	if err := e.Tick(t.Context(), f.now); err != nil || native != 1 || sent != 1 {
		t.Fatal("native PR lane did not deliver exactly once", native, sent, err)
	}
}

func TestGitHubBotMentionsAndOlderReviewContext(t *testing.T) {
	hs, s, f := githubFixtureService(t)
	configureGitHubFixture(t, s, false)
	if _, err := s.add(t.Context(), "https://github.com/org/project/pull/1", false); err != nil {
		t.Fatal(err)
	}
	ordinary := githubCommentFixture(1, "helper[bot]", "routine bot report")
	ordinary["user"].(map[string]string)["type"] = "Bot"
	mention := githubCommentFixture(2, "helper[bot]", strings.Repeat("context ", 60)+"@owner please confirm")
	mention["user"].(map[string]string)["type"] = "Bot"
	f.comments = []map[string]any{ordinary, mention}
	f.now = f.now.Add(11 * time.Minute)
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	events := githubEventsForTest(t, s)
	if len(events) != 1 || events[0].SourceID != "comment:2" {
		t.Fatal("explicit bot mention lost after preview truncation", events)
	}
}

func TestGitHubDisabledTriggerBlocksPollingAndPendingDelivery(t *testing.T) {
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
	setEnabled := func(enabled bool) {
		t.Helper()
		if err := hs.repo().transact(t.Context(), func(tx *sql.Tx) error {
			cfg, err := loadHeartbeatConfig(t.Context(), tx)
			if err != nil {
				return err
			}
			tr := cfg.Triggers["github_prs"]
			tr.Enabled = enabled
			cfg.Triggers[tr.ID] = tr
			cfg.Revision++
			return saveEntity(t.Context(), tx, "heartbeat_config", heartbeatConfigID, cfg)
		}); err != nil {
			t.Fatal(err)
		}
	}
	setEnabled(false)
	requests, sent := f.requests, 0
	if err := s.Poll(t.Context(), hs.Options); err != nil || requests != f.requests {
		t.Fatal("disabled trigger still polled", err)
	}
	e := heartbeat.Engine{Store: JobStore{Repository: s.repo()}, Gate: hs.Allows, Clock: s.Clock, AfterDelivery: s.Acknowledge, Sender: courseSender(func() { sent++ }), Runner: githubSummaryTestRunner(t, s)}
	if err := e.Tick(t.Context(), f.now); err != nil || sent != 0 {
		t.Fatal("disabled trigger leaked pending delivery", err)
	}
	setEnabled(true)
	if err := e.Tick(t.Context(), f.now); err != nil || sent != 1 {
		t.Fatal("valid pending message lost on resume", err, sent)
	}
}

func TestGitHubAutomaticallyFindsLaterNewPublicPR(t *testing.T) {
	hs, s, f := githubFixtureService(t)
	configureGitHubFixture(t, s, true)
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	watches, _ := s.Watches(t.Context())
	if len(watches) != 0 {
		t.Fatal(watches)
	}
	f.discovery = true
	f.now = f.now.Add(31 * time.Minute)
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	watches, _ = s.Watches(t.Context())
	if len(watches) != 1 || watches[0].Baseline == nil || len(githubEventsForTest(t, s)) != 0 {
		t.Fatal("new PR discovery/baseline failed", watches)
	}
}
