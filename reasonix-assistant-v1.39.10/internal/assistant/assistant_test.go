package assistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reasonix/internal/heartbeat"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) (Tasks, time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 22, 8, 0, 0, 0, time.FixedZone("CST", 8*3600))
	return Tasks{Repository: NewRepository(t.TempDir()), Options: Options{Workspace: t.TempDir(), Model: "test/model", Target: &heartbeat.Target{ConnectionID: "weixin", Domain: "weixin", ChatID: "owner", ChatType: "dm"}}, Now: func() time.Time { return now }}, now
}
func createTask(t *testing.T, s Tasks, r TaskRequest) TaskView {
	t.Helper()
	r.Action = "create"
	out, err := s.Apply(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	return out.(TaskView)
}

func TestTaskPersistenceDedupAndRevision(t *testing.T) {
	s, now := fixture(t)
	r := TaskRequest{Title: "交报告", At: now.Add(time.Hour).Format(time.RFC3339)}
	first := createTask(t, s, r)
	again := createTask(t, s, r)
	if first.ID != again.ID || first.Schedule == nil || first.Schedule.Kind != "message" {
		t.Fatalf("wrong durable reminder: %+v", first)
	}
	restarted := Tasks{Repository: NewRepository(filepath.Dir(filepath.Dir(s.Repository.Path)))}
	all, err := restarted.List(t.Context(), false)
	if err != nil || len(all) != 1 {
		t.Fatalf("restart: %v %v", all, err)
	}
	if _, err = s.Apply(t.Context(), TaskRequest{Action: "cancel", ID: first.ID, Revision: 99}); err == nil {
		t.Fatal("stale update accepted")
	}
	if _, err = s.Apply(t.Context(), TaskRequest{Action: "cancel", ID: first.ID, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	jobs, err := (JobStore{Repository: s.Repository}).List(t.Context())
	if err != nil || jobs[0].Enabled {
		t.Fatalf("cancel lost: %v %v", jobs, err)
	}
}

func TestRecurringCompleteSnoozePause(t *testing.T) {
	s, now := fixture(t)
	v := createTask(t, s, TaskRequest{Title: "吃药", Interval: "0 15 * * *", EndsAt: now.AddDate(0, 0, 7).Format(time.RFC3339)})
	out, err := s.Apply(t.Context(), TaskRequest{Action: "complete", ID: v.ID, Revision: 1, OccurrenceOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	v = out.(TaskView)
	if v.Status != "open" || v.Schedule.Due(now.Add(7*time.Hour)) {
		t.Fatal("today completion did not suppress today's reminder")
	}
	out, err = s.Apply(t.Context(), TaskRequest{Action: "snooze", ID: v.ID, Revision: 2, At: now.Add(2 * time.Hour).Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	v = out.(TaskView)
	if v.Schedule.Due(now.Add(time.Hour)) || !v.Schedule.Due(now.Add(2*time.Hour)) {
		t.Fatal("snooze did not replace next trigger")
	}
	out, err = s.Apply(t.Context(), TaskRequest{Action: "pause", ID: v.ID, Revision: 3})
	if err != nil {
		t.Fatal(err)
	}
	if out.(TaskView).Schedule.Enabled {
		t.Fatal("pause failed")
	}
	out, err = s.Apply(t.Context(), TaskRequest{Action: "resume", ID: v.ID, Revision: 4})
	if err != nil || !out.(TaskView).Schedule.Enabled {
		t.Fatal("resume failed", err)
	}
}

func TestAtomicRollbackAndConcurrentCreate(t *testing.T) {
	s, now := fixture(t)
	v := createTask(t, s, TaskRequest{Title: "提醒", At: now.Add(time.Hour).Format(time.RFC3339)})
	jobs := JobStore{Repository: s.Repository}
	if err := jobs.Update(t.Context(), v.Schedule.ID, func(job *heartbeat.Task) error { job.Prompt = "corruption"; return errors.New("abort") }); err == nil {
		t.Fatal("expected rollback")
	}
	after, err := jobs.List(t.Context())
	if err != nil || after[0].Prompt != "提醒" {
		t.Fatal("failed transaction changed job", err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := s.Apply(t.Context(), TaskRequest{Action: "create", Title: "same obligation", Kind: "todo"})
			failures <- err
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.List(t.Context(), true)
	if err != nil || len(all) != 2 {
		t.Fatalf("dedupe race: %d %v", len(all), err)
	}
}

func TestMemoryExpiryAndFreshContext(t *testing.T) {
	home := t.TempDir()
	now := time.Now().Truncate(time.Second)
	memories := Memories{Home: home, Now: func() time.Time { return now }}
	permanent, err := memories.Apply(t.Context(), MemoryRequest{Action: "create", Subject: "语言", Body: "使用中文", Kind: "long"})
	if err != nil {
		t.Fatal(err)
	}
	temporary, err := memories.Apply(t.Context(), MemoryRequest{Action: "create", Subject: "旅行", Body: "正在日本旅行", Kind: "short", ExpiresAt: now.Add(time.Hour).Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	before, err := ContextFor(t.Context(), home, now)
	if err != nil || !strings.Contains(before, "正在日本旅行") {
		t.Fatal("missing active memory", err)
	}
	after, err := ContextFor(t.Context(), home, now.Add(time.Hour))
	if err != nil || strings.Contains(after, "正在日本旅行") || !strings.Contains(after, "使用中文") {
		t.Fatal("expiry boundary or permanent memory wrong", err)
	}
	if !strings.Contains(after, "expired") {
		t.Fatal("missing explicit inactive memory status")
	}
	if _, err = memories.Apply(t.Context(), MemoryRequest{Action: "update", ID: permanent[0].ID, Revision: 99, Body: "bad"}); err == nil {
		t.Fatal("stale memory update accepted")
	}
	if _, err = memories.Apply(t.Context(), MemoryRequest{Action: "archive", ID: permanent[0].ID, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	after, err = ContextFor(t.Context(), home, now)
	if err != nil || strings.Contains(after, "使用中文") {
		t.Fatal("archived memory still active", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "assistant", "memory", temporary[0].ID+".md"))
	if err != nil || !strings.Contains(string(raw), "正在日本旅行") {
		t.Fatal("Markdown not readable", err)
	}
	info, err := os.Stat(filepath.Join(home, "assistant", "memory", temporary[0].ID+".md"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("unsafe memory permissions", err)
	}
}

func TestMemoryTimingValidation(t *testing.T) {
	now := time.Now()
	m := Memories{Home: t.TempDir(), Now: func() time.Time { return now }}
	for _, r := range []MemoryRequest{
		{Action: "create", Subject: "x", Body: "y", Kind: "short"},
		{Action: "create", Subject: "x", Body: "y", Kind: "short", ExpiresAt: now.Add(-time.Hour).Format(time.RFC3339)},
		{Action: "create", Subject: "x", Body: "y", Kind: "short", StartsAt: now.Add(2 * time.Hour).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339)},
	} {
		if _, err := m.Apply(t.Context(), r); err == nil {
			t.Fatal("invalid short memory accepted", r)
		}
	}
	_, err := m.Apply(t.Context(), MemoryRequest{Action: "create", Subject: "将来", Body: "尚未生效", Kind: "short", StartsAt: now.Add(time.Hour).Format(time.RFC3339), ExpiresAt: now.Add(2 * time.Hour).Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	active, err := m.Apply(t.Context(), MemoryRequest{Action: "list"})
	if err != nil || len(active) != 0 {
		t.Fatal("future memory active early", err)
	}
}

func TestToolsAreScopedAndBackgroundCannotMutate(t *testing.T) {
	s, _ := fixture(t)
	home := t.TempDir()
	tools := Tools(home, s.Options)
	if tools[0].ReadOnly() || tools[1].ReadOnly() {
		t.Fatal("mutating tools declared read-only")
	}
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "1")
	if _, err := tools[0].Execute(context.Background(), []byte(`{"action":"create","title":"bad","kind":"todo"}`)); err == nil {
		t.Fatal("background mutation accepted")
	}
	if _, err := tools[0].Execute(context.Background(), []byte(`{"action":"list"}`)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "")
	if _, err := tools[0].Execute(context.Background(), []byte(`{"action":"create","title":"bad","target":{"chat_id":"attacker"}}`)); err == nil {
		t.Fatal("model chose recipient")
	}
}

type blockingRunner struct{ started, release chan struct{} }

func (r blockingRunner) Run(ctx context.Context, _ heartbeat.Task) (heartbeat.Execution, error) {
	close(r.started)
	select {
	case <-r.release:
		return heartbeat.Execution{}, nil
	case <-ctx.Done():
		return heartbeat.Execution{}, ctx.Err()
	}
}

type captureSender struct{ sent chan string }

func (s captureSender) Send(_ context.Context, _ heartbeat.Target, text string) error {
	s.sent <- text
	return nil
}

func TestSlowAnalysisDoesNotBlockFixedReminder(t *testing.T) {
	s, now := fixture(t)
	createTask(t, s, TaskRequest{Title: "分析", Kind: "automation", At: now.Add(time.Second).Format(time.RFC3339)})
	createTask(t, s, TaskRequest{Title: "固定提醒", At: now.Add(time.Second).Format(time.RFC3339)})
	runner := blockingRunner{make(chan struct{}), make(chan struct{})}
	sender := captureSender{make(chan string, 1)}
	engine := heartbeat.Engine{Store: JobStore{Repository: s.Repository}, Runner: runner, Sender: sender, Clock: func() time.Time { return now.Add(2 * time.Second) }}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- engine.StartLanes(ctx, time.Second, func(err error) { t.Log(err) }) }()
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("analysis never started")
	}
	select {
	case text := <-sender.sent:
		if text != "固定提醒" {
			t.Fatal(text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fixed reminder blocked by model")
	}
	close(runner.release)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
