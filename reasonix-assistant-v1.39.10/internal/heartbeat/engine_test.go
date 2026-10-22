package heartbeat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestMissingWorkspaceDoesNotBlockOtherTasksOrManagement(t *testing.T) {
	store, task, now := fixture(t)
	if err := os.Remove(task.WorkspaceRoot); err != nil {
		t.Fatal(err)
	}
	other := task
	other.ID, other.WorkspaceRoot, other.Kind = "working", t.TempDir(), "message"
	if err := store.Add(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	runner := &testRunner{}
	if err := (Engine{Store: store, Runner: runner}).Tick(t.Context(), now); err == nil {
		t.Fatal("missing workspace was not reported")
	}
	tasks, err := store.List(t.Context())
	if err != nil || len(tasks) != 2 {
		t.Fatalf("tasks=%v error=%v", tasks, err)
	}
	if runner.calls != 0 || lastRun(&tasks[0]).Status != "failed" || lastRun(&tasks[1]).Status != "completed" {
		t.Fatalf("calls=%d tasks=%+v", runner.calls, tasks)
	}
	if err := store.Update(t.Context(), task.ID, func(t *Task) error { t.Enabled = false; return nil }); err != nil {
		t.Fatal(err)
	}
}

type testRunner struct {
	calls   int
	result  Execution
	err     error
	entered chan struct{}
	resume  chan struct{}
}

func (r *testRunner) Run(ctx context.Context, _ Task) (Execution, error) {
	r.calls++
	if r.entered != nil {
		close(r.entered)
		select {
		case <-r.resume:
		case <-ctx.Done():
			return Execution{}, ctx.Err()
		}
	}
	return r.result, r.err
}

type testSender struct {
	calls  int
	target Target
	texts  []string
	err    error
}

func (s *testSender) Send(_ context.Context, target Target, text string) error {
	s.calls++
	s.target = target
	s.texts = append(s.texts, text)
	return s.err
}
func fixture(t *testing.T) (Store, Task, time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 22, 17, 0, 0, 0, time.FixedZone("CST", 8*3600))
	store := Store{Path: filepath.Join(t.TempDir(), "heartbeat.json")}
	task := Task{ID: "task-1", Title: "report", Prompt: "prepare report", At: now.Format(time.RFC3339), Timezone: "Asia/Shanghai", Enabled: true, Kind: "agent", ApprovalMode: "read-only", WorkspaceRoot: t.TempDir(), CreatedAt: now.Add(-time.Hour).UnixMilli(), NewConversationEachRun: true}
	if err := store.Add(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	return store, task, now
}
func onlyTask(t *testing.T, s Store) Task {
	t.Helper()
	tasks, err := s.List(t.Context())
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%v err=%v", tasks, err)
	}
	return tasks[0]
}
func TestOneShotSurvivesRestartAndRunsOnlyOnce(t *testing.T) {
	store, _, now := fixture(t)
	runner := &testRunner{result: Execution{Text: "done", SessionID: "session-1"}}
	engine := Engine{Store: store, Runner: runner}
	if err := engine.Tick(t.Context(), now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 0 {
		t.Fatal("ran before due time")
	}
	if err := engine.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	restarted := Engine{Store: Store{Path: store.Path}, Runner: runner}
	if err := restarted.Tick(t.Context(), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got := onlyTask(t, store)
	if runner.calls != 1 || got.Enabled || got.History[0].Status != "completed" || got.History[0].SessionID != "session-1" {
		t.Fatalf("calls=%d task=%+v", runner.calls, got)
	}
}
func TestDeliveryRetryDoesNotRerunAgent(t *testing.T) {
	store, _, now := fixture(t)
	target := Target{ConnectionID: "weixin-weixin", ChatID: "owner", ChatType: "dm"}
	if err := store.Update(t.Context(), "task-1", func(t *Task) error { t.Target = &target; return nil }); err != nil {
		t.Fatal(err)
	}
	runner := &testRunner{result: Execution{Text: "report"}}
	sender := &testSender{err: errors.New("network unavailable")}
	engine := Engine{Store: store, Runner: runner, Sender: sender}
	if err := engine.Tick(t.Context(), now); err == nil {
		t.Fatal("missing send failure")
	}
	if got := onlyTask(t, store); got.History[0].Status != "delivery_pending" {
		t.Fatalf("task=%+v", got)
	}
	sender.err = nil
	if err := engine.Tick(t.Context(), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if sender.calls != 1 {
		t.Fatal("retried before backoff elapsed")
	}
	if err := engine.Tick(t.Context(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 1 || sender.calls != 2 || sender.target != target || sender.texts[1] != "report" {
		t.Fatalf("runner=%+v sender=%+v", runner, sender)
	}
	if got := onlyTask(t, store); got.Enabled || got.History[0].Status != "delivered" {
		t.Fatalf("task=%+v", got)
	}
}
func TestMessageReminderUsesNoAgent(t *testing.T) {
	store, _, now := fixture(t)
	if err := store.Update(t.Context(), "task-1", func(t *Task) error {
		t.Kind = "message"
		t.Target = &Target{ConnectionID: "wx", ChatID: "owner"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runner := &testRunner{}
	sender := &testSender{}
	if err := (Engine{Store: store, Runner: runner, Sender: sender}).Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 0 || sender.calls != 1 || sender.texts[0] != "prepare report" {
		t.Fatalf("runner=%+v sender=%+v", runner, sender)
	}
}
func TestWorkerLeasePreventsOverlapAndPreservesConcurrentEdit(t *testing.T) {
	store, _, now := fixture(t)
	runner := &testRunner{entered: make(chan struct{}), resume: make(chan struct{}), result: Execution{Text: "answer"}}
	engine := Engine{Store: store, Runner: runner}
	done := make(chan error, 1)
	go func() { done <- engine.Tick(t.Context(), now) }()
	<-runner.entered
	if err := (Engine{Store: store, Runner: &testRunner{}}).Tick(t.Context(), now); err == nil {
		t.Fatal("second worker acquired active lease")
	}
	if err := store.Update(t.Context(), "task-1", func(t *Task) error { t.Title = "edited while running"; t.Enabled = false; return nil }); err != nil {
		t.Fatal(err)
	}
	close(runner.resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := onlyTask(t, store); got.Title != "edited while running" || got.Enabled || got.History[0].Result != "answer" {
		t.Fatalf("lost concurrent edit: %+v", got)
	}
}
func TestRemovedTaskIsNotResurrectedByCompletion(t *testing.T) {
	store, _, now := fixture(t)
	runner := &testRunner{entered: make(chan struct{}), resume: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- (Engine{Store: store, Runner: runner}).Tick(t.Context(), now) }()
	<-runner.entered
	if err := store.Edit(t.Context(), func(tasks *[]Task) error { *tasks = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	close(runner.resume)
	<-done
	tasks, err := store.List(t.Context())
	if err != nil || len(tasks) != 0 {
		t.Fatalf("tasks=%v err=%v", tasks, err)
	}
}
func TestInterruptedRunNeedsExplicitRetry(t *testing.T) {
	store, _, now := fixture(t)
	if err := store.Update(t.Context(), "task-1", func(t *Task) error {
		t.LastRunAt = now.UnixMilli()
		t.History = []Run{{StartedAt: now.UnixMilli(), Status: "running"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runner := &testRunner{result: Execution{Text: "recovered"}}
	engine := Engine{Store: store, Runner: runner}
	if err := engine.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if got := onlyTask(t, store); runner.calls != 0 || got.Enabled || got.History[0].Status != "interrupted" {
		t.Fatalf("task=%+v", got)
	}
	if err := engine.Trigger(t.Context(), "task-1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 1 {
		t.Fatalf("calls=%d", runner.calls)
	}
}
func TestStoreConcurrentAddsDoNotLoseTasks(t *testing.T) {
	store, base, _ := fixture(t)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			task := base
			task.ID = string(rune('a' + i))
			if err := store.Add(t.Context(), task); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	tasks, err := store.List(t.Context())
	if err != nil || len(tasks) != 9 {
		t.Fatalf("tasks=%d err=%v", len(tasks), err)
	}
}

func TestAmbiguousDeliveryStopsAutomaticReplay(t *testing.T) {
	store, _, now := fixture(t)
	if err := store.Update(t.Context(), "task-1", func(t *Task) error { t.Target = &Target{ConnectionID: "wx", ChatID: "owner"}; return nil }); err != nil {
		t.Fatal(err)
	}
	runner := &testRunner{result: Execution{Text: "answer"}}
	sender := &testSender{err: ErrDeliveryUncertain}
	engine := Engine{Store: store, Runner: runner, Sender: sender}
	if err := engine.Tick(t.Context(), now); !errors.Is(err, ErrDeliveryUncertain) {
		t.Fatalf("err=%v", err)
	}
	if err := engine.Tick(t.Context(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 1 || sender.calls != 1 || onlyTask(t, store).History[0].Status != "delivery_uncertain" {
		t.Fatal("ambiguous notification replayed")
	}
}
func TestFailedAgentIsNotSentOrRepeatedAsSuccessfulOneShot(t *testing.T) {
	store, _, now := fixture(t)
	if err := store.Update(t.Context(), "task-1", func(t *Task) error { t.Target = &Target{ConnectionID: "wx", ChatID: "owner"}; return nil }); err != nil {
		t.Fatal(err)
	}
	runner := &testRunner{err: errors.New("model unavailable")}
	sender := &testSender{}
	engine := Engine{Store: store, Runner: runner, Sender: sender}
	if err := engine.Tick(t.Context(), now); err == nil {
		t.Fatal("execution failure hidden")
	}
	if err := engine.Tick(t.Context(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got := onlyTask(t, store)
	if runner.calls != 1 || sender.calls != 0 || got.Enabled || got.History[0].Status != "failed" {
		t.Fatalf("task=%+v", got)
	}
}

type advancingRunner struct {
	calls int
	now   *time.Time
}

func (r *advancingRunner) Run(context.Context, Task) (Execution, error) {
	r.calls++
	*r.now = r.now.Add(2 * time.Hour)
	return Execution{Text: "done"}, nil
}
func TestQueuedTaskRechecksExpiryAfterEarlierTaskCompletes(t *testing.T) {
	store, base, now := fixture(t)
	next := base
	next.ID = "expires-in-queue"
	next.EndsAt = now.Add(time.Hour).Format(time.RFC3339)
	if err := store.Add(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	clock := now
	runner := &advancingRunner{now: &clock}
	engine := Engine{Store: store, Runner: runner, Clock: func() time.Time { return clock }}
	if err := engine.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if runner.calls != 1 || len(tasks[1].History) != 0 {
		t.Fatalf("expired queued job executed: calls=%d task=%+v", runner.calls, tasks[1])
	}
}

func TestDeliveryRetryLimitDoesNotRerunAgent(t *testing.T) {
	store, _, now := fixture(t)
	if err := store.Update(t.Context(), "task-1", func(job *Task) error { job.Target = &Target{ConnectionID: "wx", ChatID: "owner"}; return nil }); err != nil {
		t.Fatal(err)
	}
	runner := &testRunner{result: Execution{Text: "report"}}
	sender := &testSender{err: errors.New("explicitly rejected")}
	engine := Engine{Store: store, Runner: runner, Sender: sender}
	for i := range 8 {
		if err := engine.Tick(t.Context(), now.Add(time.Duration(i)*time.Hour)); err == nil {
			t.Fatal("expected failed delivery")
		}
	}
	got := onlyTask(t, store)
	if got.History[0].Status != "delivery_failed" || got.Enabled || runner.calls != 1 || sender.calls != 8 {
		t.Fatalf("unexpected final delivery state: %+v", got)
	}
	if err := engine.Tick(t.Context(), now.Add(9*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if sender.calls != 8 {
		t.Fatal("exceeded retry limit")
	}
}
