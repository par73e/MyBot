package assistant

import (
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/heartbeat"
)

func TestGitHubPublicURLsAndPrivateCredential(t *testing.T) {
	_, s, _ := githubFixtureService(t)
	for _, raw := range []string{"http://github.com/org/project/pull/1", "https://evil.example/org/project/pull/1", "https://github.com/org/project/pull/1?token=x", "https://github.com/org/../pull/1", "https://github.com/org/project/pull/0", "https://github.com/org%2fsecret/project/pull/1", "https://github.com@evil.example/org/project/pull/1"} {
		if _, _, err := parseGitHubPR(raw); err == nil {
			t.Fatal("accepted unsafe URL", raw)
		}
	}
	if err := os.Chmod(s.tokenFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshot(t.Context(), "https://github.com/org/project/pull/1"); err == nil || strings.Contains(err.Error(), "fixture-token-value") {
		t.Fatal(err)
	}
}

func TestGitHubPagination304BaselineAndPartialFailure(t *testing.T) {
	hs, s, f := githubFixtureService(t)
	configureGitHubFixture(t, s, false)
	f.pages = true
	f.comments = []map[string]any{githubCommentFixture(1, "reviewer", "old first"), githubCommentFixture(2, "reviewer", "old second")}
	w, err := s.add(t.Context(), "https://github.com/org/project/pull/1", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Baseline.Items) != 2 || len(githubEventsForTest(t, s)) != 0 {
		t.Fatal("baseline replayed history", w)
	}
	got, err := s.Snapshot(t.Context(), w.URL)
	if err != nil || len(got.Items) != 2 || f.notModified < 2 {
		t.Fatal("304 lost cached page link", got, err, f.notModified)
	}
	f.now = f.now.Add(11 * time.Minute)
	f.comments[1]["body"] = "edited second reply"
	f.failPath = "/pulls/1/comments"
	f.failCode = 404
	if err := s.Poll(t.Context(), hs.Options); err == nil {
		t.Fatal("partial source read was called complete")
	}
	if len(githubEventsForTest(t, s)) != 0 {
		t.Fatal("partial read advanced baseline")
	}
	f.failPath = ""
	f.now = f.now.Add(3 * time.Minute)
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	events := githubEventsForTest(t, s)
	if len(events) != 1 || !strings.Contains(events[0].Text, "edited second reply") {
		t.Fatal(events)
	}
	f.now = f.now.Add(11 * time.Minute)
	if err := s.Poll(t.Context(), hs.Options); err != nil || len(githubEventsForTest(t, s)) != 1 {
		t.Fatal("repeat fetch duplicated edit", err)
	}
}

func TestGitHubDiscoveryScopeBaselineAndRemoval(t *testing.T) {
	hs, s, f := githubFixtureService(t)
	configureGitHubFixture(t, s, true)
	f.discovery = true
	f.incomplete = true
	if _, err := s.discover(t.Context(), true); err == nil {
		t.Fatal("incomplete search accepted")
	}
	watches, _ := s.Watches(t.Context())
	if len(watches) != 0 {
		t.Fatal("partial discovery changed scope")
	}
	f.incomplete = false
	if n, err := s.discover(t.Context(), true); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	if len(githubEventsForTest(t, s)) != 0 {
		t.Fatal("automatic baseline replayed old events")
	}
	watches, _ = s.Watches(t.Context())
	w := watches[0]
	if w.Baseline == nil {
		t.Fatal("discovery did not collect baseline")
	}
	if _, err := s.remove(t.Context(), GitHubRequest{ID: w.ID, Revision: w.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.discover(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	watches, _ = s.Watches(t.Context())
	if watches[0].Enabled || !watches[0].Excluded {
		t.Fatal("discovery undid explicit removal")
	}
}

func TestGitHubBackoffAndReadOnlyQuery(t *testing.T) {
	_, s, f := githubFixtureService(t)
	configureGitHubFixture(t, s, false)
	w, err := s.add(t.Context(), "https://github.com/org/project/pull/1", false)
	if err != nil {
		t.Fatal(err)
	}
	f.comments = append(f.comments, githubCommentFixture(1, "reviewer", "fresh reply"))
	if _, err := s.Apply(t.Context(), GitHubRequest{Action: "query", ID: w.ID}); err != nil {
		t.Fatal(err)
	}
	if len(githubEventsForTest(t, s)) != 0 {
		t.Fatal("foreground query acknowledged unseen events")
	}
	f.failPath = "/pulls/1"
	f.failCode = 429
	if _, err := s.Snapshot(t.Context(), w.URL); err == nil || strings.Contains(err.Error(), "fixture-token-value") {
		t.Fatal(err)
	}
	count := f.requests
	f.now = f.now.Add(61 * time.Second)
	if _, err := s.Snapshot(t.Context(), w.URL); err == nil || f.requests != count {
		t.Fatal("ignored rate reset", err)
	}
	f.now = f.now.Add(time.Minute)
	f.failPath = ""
	if _, err := s.Snapshot(t.Context(), w.URL); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubNotificationsDeliveryRestartAndRoutine(t *testing.T) {
	hs, s, f := githubFixtureService(t)
	configureGitHubFixture(t, s, false)
	w, err := s.add(t.Context(), "https://github.com/org/project/pull/1", false)
	if err != nil {
		t.Fatal(err)
	}
	f.comments = []map[string]any{githubCommentFixture(1, "owner", "my own reply"), githubCommentFixture(2, "reviewer", "please add a boundary test")}
	f.now = f.now.Add(11 * time.Minute)
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	sends := 0
	engine := heartbeat.Engine{Store: JobStore{Repository: s.repo()}, Gate: hs.Allows, Sender: courseSender(func() { sends++ }), Clock: s.Clock, AfterDelivery: s.Acknowledge, Runner: githubSummaryTestRunner(t, s)}
	if err := engine.Tick(t.Context(), f.now); err != nil {
		t.Fatal(err)
	}
	events := githubEventsForTest(t, s)
	if sends != 1 || len(events) != 1 || events[0].DeliveredAt.IsZero() {
		t.Fatal("notification not acknowledged", sends, events)
	}
	engine = heartbeat.Engine{Store: JobStore{Repository: s.repo()}, Gate: hs.Allows, Sender: courseSender(func() { sends++ }), Clock: s.Clock, AfterDelivery: s.Acknowledge, Runner: githubSummaryTestRunner(t, s)}
	if err := engine.Tick(t.Context(), f.now); err != nil || sends != 1 {
		t.Fatal("restart duplicated notification", sends, err)
	}
	f.now = timeMust("2026-10-27T23:31:00+08:00")
	f.comments = append(f.comments, githubCommentFixture(3, "reviewer", "night comment"))
	count := f.requests
	if err := s.Poll(t.Context(), hs.Options); err != nil || count != f.requests {
		t.Fatal("outside-routine poll ran", err)
	}
	f.now = timeMust("2026-10-28T09:50:00+08:00")
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	if err := engine.Tick(t.Context(), f.now); err != nil || sends != 2 {
		t.Fatal("next routine missed overnight changes", sends, err)
	}
	watches, _ := s.Watches(t.Context())
	w = watches[0]
	if _, err := s.remove(t.Context(), GitHubRequest{ID: w.ID, Revision: w.Revision}); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubBackgroundCannotChangeTracking(t *testing.T) {
	_, s, _ := githubFixtureService(t)
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "1")
	for _, action := range []string{"configure", "add", "remove", "discover"} {
		if _, err := s.Apply(t.Context(), GitHubRequest{Action: action, Login: "owner", URL: "https://github.com/org/project/pull/1"}); err == nil {
			t.Fatal("background changed tracking", action)
		}
	}
	if _, err := s.Apply(t.Context(), GitHubRequest{Action: "status"}); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubPauseDoesNotRequireWorkingCredential(t *testing.T) {
	hs, s, f := githubFixtureService(t)
	cfg := configureGitHubFixture(t, s, false)
	if err := os.Chmod(s.tokenFile, 0o644); err != nil {
		t.Fatal(err)
	}
	count := f.requests
	enabled := false
	if _, err := s.configure(t.Context(), GitHubRequest{Revision: cfg.Revision, Enabled: &enabled}); err != nil || count != f.requests {
		t.Fatal("cannot pause while token access is broken", err)
	}
	hb, err := hs.Config(t.Context())
	if err != nil || hb.Triggers["github_prs"].Enabled || hb.ModuleEnabled["github_pr_check"] {
		t.Fatal(hb, err)
	}
}
