package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"reasonix/internal/assistant"
	"reasonix/internal/config"
	"reasonix/internal/heartbeat"
)

func morningRunnerFixture(t *testing.T, lifetime time.Duration) (heartbeatProcessRunner, heartbeat.Task, assistant.JobStore) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fixture process is Unix only")
	}
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	store := assistant.JobStore{Repository: assistant.NewRepository(config.MemoryUserDir())}
	if _, err := store.List(t.Context()); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	job := heartbeat.Task{ID: "morning-fixture", Title: "morning", Prompt: "fixture morning", ModuleID: "morning_briefing", Capability: "morning", TriggerID: "morning", TriggerRevision: 1, OccurrenceID: "occ", Enabled: true, At: now.Add(-time.Second).Format(time.RFC3339), EndsAt: now.Add(lifetime).Format(time.RFC3339), Timezone: "Asia/Shanghai", WorkspaceRoot: home, ApprovalMode: "read-only", Kind: "agent", DecideNotification: true, NewConversationEachRun: true, RunStartedAt: 123, Target: &heartbeat.Target{ConnectionID: "fixture", ChatID: "owner", ChatType: "dm"}}
	if err := store.Add(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	week := heartbeat.WeeklySchedule{}
	for _, d := range []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"} {
		week[d] = heartbeat.DailyWindow{Enabled: true, Start: "00:00", End: "24:00"}
	}
	cfg := assistant.HeartbeatConfig{Revision: 1, Timezone: "Asia/Shanghai", Weekly: week, Overrides: map[string]assistant.HeartbeatOverride{}, ModuleEnabled: map[string]bool{"morning_briefing": true}, Triggers: map[string]assistant.ModuleTrigger{"morning": {ID: "morning", Title: "morning", Enabled: true, Mode: "daily", RoutineBound: true, Modules: []string{"morning_briefing"}, CatchUpSeconds: 300}}}
	for _, module := range assistant.HeartbeatModules() {
		cfg.ModuleEnabled[module.ID] = module.ID == "morning_briefing"
	}
	snap := assistant.MorningSnapshot{ID: job.ID, Date: now.Format("2006-01-02"), CreatedAt: now, MailStatus: "paused", MailIDs: []string{}, News: assistant.NewsPage{Status: "ok", Items: []assistant.NewsItem{{ID: "news1"}}}}
	occ := assistant.ModuleOccurrence{ID: "occ", Revision: 1, TriggerID: "morning", Date: snap.Date, ScheduledAt: now, Modules: []string{"morning_briefing"}, JobIDs: []string{job.ID}}
	db, err := sql.Open("sqlite", store.Repository.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, v := range []struct {
		kind, id string
		data     any
	}{{"heartbeat_config", "default", cfg}, {"morning_snapshot", job.ID, snap}, {"module_occurrence", "occ", occ}} {
		raw, _ := json.Marshal(v.data)
		if _, err = db.Exec("INSERT INTO assistant_entities(kind,id,payload) VALUES(?,?,?)", v.kind, v.id, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("REASONIX_ASSISTANT_MORNING_BACKGROUND", "1")
	t.Setenv("REASONIX_ASSISTANT_JOB_ID", job.ID)
	t.Setenv("REASONIX_ASSISTANT_MORNING_ATTEMPT", "123")
	if _, err = (assistant.MorningTool{Service: assistant.MorningService{Home: home}}).Execute(t.Context(), json.RawMessage(`{"action":"snapshot"}`)); err != nil {
		t.Fatal(err)
	}
	return heartbeatProcessRunner{Executable: filepath.Join(home, "fake-agent")}, job, store
}

func writeMorningAgent(t *testing.T, runner heartbeatProcessRunner, body string) {
	t.Helper()
	if err := os.WriteFile(runner.Executable, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestMorningRunnerRepairsInSameSession(t *testing.T) {
	runner, job, _ := morningRunnerFixture(t, 5*time.Minute)
	home := filepath.Dir(runner.Executable)
	for i, summary := range []string{strings.Repeat("长", 26), "官方发布新模型"} {
		draft, _ := json.Marshal(assistant.MorningOutput{Weather: "暂未获取", MailDecisions: []assistant.MailDecision{}, News: []assistant.MorningNews{{ID: "news1", Summary: summary}}})
		result, _ := json.Marshal(heartbeatAgentResult{Type: "result", Result: string(draft), SessionID: "fixture-session"})
		name := []string{"first.json", "second.json"}[i]
		if err := os.WriteFile(filepath.Join(home, name), result, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeMorningAgent(t, runner, `printf '%s\n' "$*" >> "$REASONIX_HOME/calls"
case "$*" in
  *"--resume fixture-session"*) cat "$REASONIX_HOME/second.json" ;;
  *) cat "$REASONIX_HOME/first.json" ;;
esac
`)
	out, err := runner.Run(t.Context(), job)
	if err != nil || out.DecisionReason != "早安Friday" || !strings.Contains(out.Text, "官方发布新模型") {
		t.Fatal(out, err)
	}
	calls, err := os.ReadFile(filepath.Join(home, "calls"))
	if err != nil || strings.Count(string(calls), "--output-format") != 2 || !strings.Contains(string(calls), "validation_error") || !strings.Contains(string(calls), "--resume fixture-session") {
		t.Fatal("repair did not resume with validation feedback", string(calls), err)
	}
}

type morningTestSender struct{ calls int }

func (s *morningTestSender) Send(context.Context, heartbeat.Target, string) error {
	s.calls++
	return nil
}

func TestMorningRunnerModelFailureDeliversOnceAcrossRestart(t *testing.T) {
	runner, job, store := morningRunnerFixture(t, 5*time.Minute)
	writeMorningAgent(t, runner, `printf '%s\n' 'not JSON'`)
	sender := &morningTestSender{}
	service := assistant.MorningService{Home: config.MemoryUserDir()}
	newEngine := func() heartbeat.Engine {
		return heartbeat.Engine{Store: store, Runner: runner, Sender: sender, Gate: (assistant.HeartbeatService{Home: config.MemoryUserDir()}).Allows, AfterDelivery: service.Acknowledge}
	}
	engine := newEngine()
	if err := engine.Tick(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.List(t.Context())
	if err != nil || len(jobs) != 1 || jobs[0].History[0].Status != "delivered" || !strings.Contains(jobs[0].History[0].Result, "邮件自动检查还暂停着") {
		t.Fatal(jobs, err)
	}
	engine = newEngine()
	if err := engine.Tick(t.Context(), time.Now()); err != nil || sender.calls != 1 {
		t.Fatal("fallback replayed after restart", sender.calls, err)
	}
	latest, err := (assistant.MorningTool{Service: service}).Execute(t.Context(), json.RawMessage(`{"action":"latest"}`))
	if err != nil || !strings.Contains(latest, `"fallback":true`) || !strings.Contains(latest, job.ID) {
		t.Fatal(latest, err)
	}
}

func TestMorningRunnerReservesFallbackTimeForHungModel(t *testing.T) {
	runner, job, _ := morningRunnerFixture(t, 22*time.Second)
	writeMorningAgent(t, runner, "exec sleep 60\n")
	started := time.Now()
	out, err := runner.Run(t.Context(), job)
	if err != nil || !strings.Contains(out.DecisionReason, "降级") || time.Since(started) > 10*time.Second || job.Expired(time.Now()) {
		t.Fatal("model consumed fallback/delivery budget", out, err, time.Since(started))
	}
}

func TestMorningRunnerParentCancellationDoesNotFallback(t *testing.T) {
	runner, job, _ := morningRunnerFixture(t, 5*time.Minute)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if out, err := runner.Run(ctx, job); err == nil || out.Text != "" {
		t.Fatal("cancelled parent produced fallback", out, err)
	}
}
