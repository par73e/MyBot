package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"reasonix/internal/heartbeat"
)

func githubSummaryTestRunner(t *testing.T, s GitHubService) heartbeat.Runner {
	t.Helper()
	return mailTestRunner(func(ctx context.Context, job heartbeat.Task) (heartbeat.Execution, error) {
		snap, err := s.notificationSnapshot(ctx, job, true)
		if err != nil {
			return heartbeat.Execution{}, err
		}
		values := []GitHubSummary{}
		for _, event := range snap.Events {
			if githubDiscussion(event.Kind) {
				values = append(values, GitHubSummary{ID: event.ID, Summary: "对方提出了代码审查意见，请查看具体要求。", EvidenceQuote: boundedNewsText(event.Body, 100)})
			}
		}
		raw, _ := json.Marshal(map[string]any{"summaries": values})
		text, err := s.FinalizeNotification(ctx, job, string(raw), false)
		return heartbeat.Execution{Text: text}, err
	})
}

func githubSummaryFixture(t *testing.T, body string) (HeartbeatService, GitHubService, *githubFixture, heartbeat.Task) {
	t.Helper()
	hs, s, f := githubFixtureService(t)
	configureGitHubFixture(t, s, false)
	if _, err := s.add(t.Context(), "https://github.com/org/project/pull/1", false); err != nil {
		t.Fatal(err)
	}
	f.comments = []map[string]any{githubCommentFixture(1, "reviewer", body)}
	f.now = f.now.Add(time.Minute)
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	jobs, err := (JobStore{Repository: s.repo()}).List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.ModuleID == "github_pr_notification" {
			job.RunStartedAt = 123
			if err := (JobStore{Repository: s.repo()}).Update(t.Context(), job.ID, func(saved *heartbeat.Task) error {
				saved.History = []heartbeat.Run{{StartedAt: 123, Status: "running"}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			return hs, s, f, job
		}
	}
	t.Fatal("notification missing")
	return hs, s, f, heartbeat.Task{}
}

func TestGitHubChineseSummaryUsesLongBodyAndBoundRead(t *testing.T) {
	body := strings.Repeat("The implementation looks promising.\n", 100) + "Please add a regression test for empty input."
	_, s, _, job := githubSummaryFixture(t, body)
	if _, err := s.FinalizeNotification(t.Context(), job, `{"summaries":[]}`, false); err == nil {
		t.Fatal("accepted unread source")
	}
	t.Setenv("REASONIX_ASSISTANT_GITHUB_BACKGROUND", "1")
	t.Setenv("REASONIX_ASSISTANT_JOB_ID", job.ID)
	t.Setenv("REASONIX_ASSISTANT_GITHUB_ATTEMPT", "123")
	raw, err := (GitHubNotificationTool{Service: s}).Execute(t.Context(), json.RawMessage(`{"action":"snapshot"}`))
	if err != nil || !strings.Contains(raw, "Please add a regression test for empty input.") || strings.Contains(raw, "fixture-token-value") {
		t.Fatal("long reply lost or credential leaked", err)
	}
	values, _ := json.Marshal(map[string]any{"summaries": []GitHubSummary{{ID: job.GitHubEventIDs[0], Summary: "认可实现方向，但希望补充空输入的回归测试。", EvidenceQuote: "Please add a regression test for empty input."}}})
	text, err := s.FinalizeNotification(t.Context(), job, string(values), false)
	if err != nil || !strings.Contains(text, "reviewer有新回复：认可实现方向") || strings.Contains(text, "The implementation") || !strings.Contains(text, "#issuecomment-1") {
		t.Fatal(text, err)
	}
	other := job
	other.RunStartedAt++
	if _, err := s.notificationSnapshot(t.Context(), other, true); err == nil {
		t.Fatal("accepted another attempt")
	}
}

func TestGitHubSummaryValidationAndChineseFallback(t *testing.T) {
	_, s, _, job := githubSummaryFixture(t, "Please test the empty input.")
	if _, err := s.notificationSnapshot(t.Context(), job, true); err != nil {
		t.Fatal(err)
	}
	valid := GitHubSummary{ID: job.GitHubEventIDs[0], Summary: "要求补充空输入测试。", EvidenceQuote: "Please test the empty input."}
	for _, mutate := range []func(*GitHubSummary){
		func(v *GitHubSummary) { v.ID = "another-event" },
		func(v *GitHubSummary) { v.Summary = "Please test empty input." },
		func(v *GitHubSummary) { v.Summary = strings.Repeat("中", 181) },
		func(v *GitHubSummary) { v.EvidenceQuote = "invented quote" },
		func(v *GitHubSummary) { v.Summary = "查看 https://evil.example" },
	} {
		value := valid
		mutate(&value)
		raw, _ := json.Marshal(map[string]any{"summaries": []GitHubSummary{value}})
		if _, err := s.FinalizeNotification(t.Context(), job, string(raw), false); err == nil {
			t.Fatal("accepted invalid summary", value)
		}
	}
	for _, values := range [][]GitHubSummary{nil, {valid, valid}} {
		raw, _ := json.Marshal(map[string]any{"summaries": values})
		if _, err := s.FinalizeNotification(t.Context(), job, string(raw), false); err == nil {
			t.Fatal("accepted missing/duplicated reply")
		}
	}
	text, err := s.FinalizeNotification(t.Context(), job, "", true)
	if err != nil || strings.Contains(text, "Please test") || !strings.Contains(text, "中文摘要暂不可用") || !strings.Contains(text, "#issuecomment-1") {
		t.Fatal("fallback leaked English or lost link", text, err)
	}
}

func TestGitHubSummaryCancelledWhenReplyEdited(t *testing.T) {
	hs, s, f, job := githubSummaryFixture(t, "Initial reply")
	if _, err := s.notificationSnapshot(t.Context(), job, true); err != nil {
		t.Fatal(err)
	}
	f.comments[0]["body"] = "Updated requirements"
	f.now = f.now.Add(time.Minute)
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinalizeNotification(t.Context(), job, "", true); err == nil {
		t.Fatal("stale reply allowed through fallback")
	}
}

func TestGitHubSummaryTruncationIsExplicitAndWithinToolBudget(t *testing.T) {
	_, s, _, job := githubSummaryFixture(t, strings.Repeat("<中文代码审查>\n", 4000))
	snap, err := s.notificationSnapshot(t.Context(), job, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(snap)
	if len(raw) > 30*1024 || !snap.Events[0].BodyTruncated || snap.Events[0].Body == "" {
		t.Fatal("oversized body not safely bounded", len(raw))
	}
	out, _ := json.Marshal(map[string]any{"summaries": []GitHubSummary{{ID: job.GitHubEventIDs[0], Summary: "对方留下了中文代码审查内容。", EvidenceQuote: "中文代码审查"}}})
	text, err := s.FinalizeNotification(t.Context(), job, string(out), false)
	if err != nil || !strings.Contains(text, "依据已获取的部分正文") {
		t.Fatal(text, err)
	}
}

func TestGitHubSummaryBatchesDoNotAcknowledgeHiddenReplies(t *testing.T) {
	hs, s, f := githubFixtureService(t)
	configureGitHubFixture(t, s, false)
	if _, err := s.add(t.Context(), "https://github.com/org/project/pull/1", false); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 6; i++ {
		f.comments = append(f.comments, githubCommentFixture(i, "reviewer", "Please add a test."))
	}
	f.now = f.now.Add(time.Minute)
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	sends := 0
	e := heartbeat.Engine{Store: JobStore{Repository: s.repo()}, Gate: hs.Allows, Clock: s.Clock, AfterDelivery: s.Acknowledge, Sender: courseSender(func() { sends++ }), Runner: githubSummaryTestRunner(t, s)}
	if err := e.Tick(t.Context(), f.now); err != nil {
		t.Fatal(err)
	}
	delivered := func() int {
		count := 0
		for _, event := range githubEventsForTest(t, s) {
			if !event.DeliveredAt.IsZero() {
				count++
			}
		}
		return count
	}
	if delivered() != 5 || sends != 1 {
		t.Fatal("first batch acknowledged unseen replies", delivered(), sends)
	}
	f.now = f.now.Add(time.Minute)
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	if err := e.Tick(t.Context(), f.now); err != nil || delivered() != 6 || sends != 2 {
		t.Fatal("remaining reply was lost", err, delivered(), sends)
	}
}
