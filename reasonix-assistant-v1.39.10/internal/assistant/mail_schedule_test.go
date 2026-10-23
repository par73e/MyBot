package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"reasonix/internal/heartbeat"
)

func TestMailScheduleOwnerChangesPersistAndGateEveryStage(t *testing.T) {
	s, o := mailFixture(t)
	policy, err := s.EnsureSchedule(t.Context(), o)
	if err != nil || policy.Kind != "mail_check" || !policy.Due(time.Now()) {
		t.Fatal(policy, err)
	}
	tool := MailScheduleTool{Service: s}
	raw, err := tool.Execute(t.Context(), json.RawMessage(`{"action":"update","revision":1,"timeWindowStart":"08:00","timeWindowEnd":"00:00","timezone":"Asia/Shanghai"}`))
	if err != nil || !strings.Contains(raw, `"revision":2`) {
		t.Fatal(raw, err)
	}
	if _, err = tool.Execute(t.Context(), json.RawMessage(`{"action":"pause","revision":1}`)); err == nil {
		t.Fatal("stale revision accepted")
	}
	reloaded, err := NewMailService(s.Home).EnsureSchedule(t.Context(), o)
	if err != nil || reloaded.Revision != 2 || reloaded.TimeWindowStart != "08:00" {
		t.Fatal("restart reset owner policy", reloaded, err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	for _, tc := range []struct {
		hour, min int
		want      bool
	}{{0, 0, false}, {7, 59, false}, {8, 0, true}, {23, 59, true}} {
		now := time.Date(2026, 10, 24, tc.hour, tc.min, 0, 0, loc)
		for _, task := range []heartbeat.Task{reloaded, newMailJob("child", "read", Options{Workspace: o.Workspace, MailScheduleID: MailScheduleID}, now, nil)} {
			allowed, e := s.ScheduleAllows(t.Context(), task, now)
			if e != nil || allowed != tc.want {
				t.Fatalf("%02d:%02d kind=%s allowed=%v err=%v", tc.hour, tc.min, task.Kind, allowed, e)
			}
		}
	}
	if _, err = tool.Execute(t.Context(), json.RawMessage(`{"action":"pause","revision":2}`)); err != nil {
		t.Fatal(err)
	}
	daytime := time.Date(2026, 10, 24, 12, 0, 0, 0, loc)
	if allowed, _ := s.ScheduleAllows(t.Context(), heartbeat.Task{Capability: "mail"}, daytime); allowed {
		t.Fatal("paused child admitted")
	}
	if allowed, _ := s.ScheduleAllows(t.Context(), heartbeat.Task{Kind: "message"}, daytime); !allowed {
		t.Fatal("mail pause blocked other reminders")
	}
	if _, err = tool.Execute(t.Context(), json.RawMessage(`{"action":"resume","revision":3}`)); err != nil {
		t.Fatal(err)
	}
	if allowed, _ := s.ScheduleAllows(t.Context(), heartbeat.Task{Capability: "mail"}, daytime); !allowed {
		t.Fatal("resume did not apply live")
	}
}

func TestMailScheduleValidationAndBackgroundDenial(t *testing.T) {
	s, o := mailFixture(t)
	if _, err := s.EnsureSchedule(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	tool := MailScheduleTool{Service: s}
	for _, args := range []string{
		`{"action":"update","revision":1,"interval_seconds":1}`,
		`{"action":"update","revision":1,"timezone":"Unknown/Zone"}`,
		`{"action":"update","revision":1,"timeWindowStart":"08:00"}`,
		`{"action":"update","revision":1,"timeWindowStart":"08:00","timeWindowEnd":"08:00"}`,
		`{"action":"update","revision":1,"timeWindowStart":"24:00","timeWindowEnd":"08:00"}`,
		`{"action":"update","revision":1,"clear_window":true,"timeWindowStart":"08:00","timeWindowEnd":"00:00"}`,
	} {
		if _, err := tool.Execute(t.Context(), json.RawMessage(args)); err == nil {
			t.Fatal("accepted invalid policy", args)
		}
	}
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"action":"update","revision":1,"timeWindowStart":"22:00","timeWindowEnd":"08:00"}`)); err != nil {
		t.Fatal(err)
	}
	policy, _ := s.MailSchedule(t.Context())
	loc, _ := time.LoadLocation(policy.Timezone)
	for _, tc := range []struct {
		hour int
		want bool
	}{{21, false}, {22, true}, {0, true}, {8, false}} {
		if policy.WithinTimeWindow(time.Date(2026, 10, 24, tc.hour, 0, 0, 0, loc)) != tc.want {
			t.Fatal("cross-midnight window", tc)
		}
	}
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "1")
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"action":"resume","revision":2}`)); err == nil {
		t.Fatal("background modified schedule")
	}
}

func TestMailNativeHeartbeatChecksWithoutModelAndDefersPendingPush(t *testing.T) {
	s, o := mailFixture(t)
	if _, err := s.EnsureSchedule(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.Transport = func(context.Context, map[string]any) (mailWire, error) {
		calls++
		return mailWire{Results: []mailResult{goodResult("qq", MailMessage{TransportID: "1", Subject: "test"}), goodResult("gmail1"), goodResult("gmail2")}}, nil
	}
	modelCalls, sends := 0, 0
	now := time.Now()
	e := heartbeat.Engine{Store: JobStore{Repository: s.Repository}, Gate: s.ScheduleAllows, Clock: func() time.Time { return now }, Checker: mailTestRunner(func(ctx context.Context, task heartbeat.Task) (heartbeat.Execution, error) {
		o.MailScheduleID = task.ID
		_, err := s.Poll(ctx, o)
		return heartbeat.Execution{}, err
	}), Runner: mailTestRunner(func(context.Context, heartbeat.Task) (heartbeat.Execution, error) {
		modelCalls++
		// Policy changes while the analysis is finishing; send must use latest state.
		_, err := (MailScheduleTool{Service: s}).Execute(t.Context(), json.RawMessage(`{"action":"pause","revision":1}`))
		return heartbeat.Execution{Text: "notice"}, err
	}), Sender: mailTestSender(func() { sends++ })}
	if err := e.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || modelCalls != 0 {
		t.Fatal("check invoked model", calls, modelCalls)
	}
	now = now.Add(time.Second)
	if err := e.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if modelCalls != 1 || sends != 0 {
		t.Fatal("paused pending push delivered", modelCalls, sends)
	}
	if _, err := (MailScheduleTool{Service: s}).Execute(t.Context(), json.RawMessage(`{"action":"resume","revision":2}`)); err != nil {
		t.Fatal(err)
	}
	if err := e.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if sends != 1 || modelCalls != 1 || calls != 1 {
		t.Fatal("resume replayed analysis/check", sends, modelCalls, calls)
	}
}

type mailTestRunner func(context.Context, heartbeat.Task) (heartbeat.Execution, error)

func (f mailTestRunner) Run(ctx context.Context, t heartbeat.Task) (heartbeat.Execution, error) {
	return f(ctx, t)
}

type mailTestSender func()

func (f mailTestSender) Send(context.Context, heartbeat.Target, string) error { f(); return nil }

func TestMailPauseDuringNativeCheckCannotResumeItself(t *testing.T) {
	s, o := mailFixture(t)
	if _, err := s.EnsureSchedule(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	s.Transport = func(ctx context.Context, _ map[string]any) (mailWire, error) {
		close(entered)
		<-ctx.Done()
		return mailWire{}, ctx.Err()
	}
	e := heartbeat.Engine{Store: JobStore{Repository: s.Repository}, Gate: s.ScheduleAllows, Checker: mailTestRunner(func(ctx context.Context, task heartbeat.Task) (heartbeat.Execution, error) {
		o.MailScheduleID = task.ID
		_, err := s.Poll(ctx, o)
		return heartbeat.Execution{}, err
	})}
	done := make(chan error, 1)
	go func() { done <- e.Tick(t.Context(), time.Now()) }()
	<-entered
	if _, err := (MailScheduleTool{Service: s}).Execute(t.Context(), json.RawMessage(`{"action":"pause","revision":1}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("native check did not stop")
	}
	policy, err := s.MailSchedule(t.Context())
	if err != nil || policy.Enabled || policy.Revision != 2 || policy.History[len(policy.History)-1].Status != "deferred" {
		t.Fatal("cancelled check overwrote owner pause", policy, err)
	}
}
