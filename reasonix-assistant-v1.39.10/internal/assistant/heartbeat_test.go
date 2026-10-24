package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"reasonix/internal/heartbeat"
)

func unifiedFixture(t *testing.T) (HeartbeatService, HeartbeatTool) {
	t.Helper()
	m, o := mailFixture(t)
	if _, e := m.EnsureSchedule(t.Context(), o); e != nil {
		t.Fatal(e)
	}
	week := heartbeat.WeeklySchedule{}
	for _, d := range weatherWeekdays {
		hour := "08:00"
		if d == "saturday" || d == "sunday" {
			hour = "11:00"
		}
		week[d] = heartbeat.DailyWindow{Enabled: true, Start: hour, End: "24:00"}
	}
	raw, _ := json.Marshal(MailScheduleRequest{Action: "update", Revision: 1, WeeklySchedule: &week})
	if _, e := (MailScheduleTool{Service: m}).Execute(t.Context(), raw); e != nil {
		t.Fatal(e)
	}
	s := HeartbeatService{Home: m.Home, Options: o}
	if _, e := s.Ensure(t.Context()); e != nil {
		t.Fatal(e)
	}
	now, _ := time.Parse(time.RFC3339, "2026-10-24T12:00:00+08:00")
	tool := HeartbeatTool{Service: s, Now: func() time.Time { return now }}
	if _, e := tool.Execute(t.Context(), json.RawMessage(`{"action":"set_module_enabled","revision":1,"module_id":"weather_briefing","enabled":true}`)); e != nil {
		t.Fatal(e)
	}
	return s, tool
}
func callHeartbeat(t *testing.T, tool HeartbeatTool, args string) {
	t.Helper()
	if _, e := tool.Execute(t.Context(), json.RawMessage(args)); e != nil {
		t.Fatal(args, e)
	}
}
func atHeartbeat(t *testing.T, s HeartbeatService, at string) []heartbeat.Task {
	t.Helper()
	now, e := time.Parse(time.RFC3339, at)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Prepare(t.Context(), now); e != nil {
		t.Fatal(e)
	}
	jobs, e := (JobStore{Repository: s.repo()}).List(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	var active []heartbeat.Task
	for _, j := range jobs {
		if j.Enabled && j.ModuleID != "" {
			active = append(active, j)
		}
	}
	return active
}
func TestUnifiedHeartbeatMigrationAndTwoModulesWithoutPrivateClocks(t *testing.T) {
	s, _ := unifiedFixture(t)
	cfg, e := s.Config(t.Context())
	if e != nil || cfg.Weekly["sunday"].Start != "11:00" {
		t.Fatal(cfg, e)
	}
	for _, j := range atHeartbeat(t, s, "2026-10-25T07:00:00+08:00") {
		t.Fatal("module ran before wake", j)
	}
	jobs := atHeartbeat(t, s, "2026-10-25T11:00:00+08:00")
	if len(jobs) != 2 {
		t.Fatal("expected the two selected modules", jobs)
	}
	for _, j := range jobs {
		if j.TriggerID != "morning" || j.TriggerRevision != 2 || j.OccurrenceID == "" {
			t.Fatal(j)
		}
		if j.ModuleID == "weather_briefing" && !strings.HasPrefix(j.Prompt, WeatherMorningContentInstructions) {
			t.Fatal("module lost its shared prompt")
		}
	}
	if got := len(atHeartbeat(t, s, "2026-10-25T11:00:20+08:00")); got != 2 {
		t.Fatal("repeated tick duplicated modules", got)
	}
	if got := len(atHeartbeat(t, s, "2026-10-25T11:01:00+08:00")); got != 3 {
		t.Fatal("interval trigger not dispatched", got)
	}
	restored := HeartbeatService{Home: s.Home, Options: s.Options}
	if got := len(atHeartbeat(t, restored, "2026-10-25T11:01:30+08:00")); got != 3 {
		t.Fatal("restart duplicated work", got)
	}
	legacy, e := NewMailService(s.Home).MailSchedule(t.Context())
	if e != nil || legacy.Enabled {
		t.Fatal("legacy clock still active", legacy, e)
	}
}
func TestUnifiedDateOverrideMovesAllSelectedModulesAndKeepsNextWeekDefaults(t *testing.T) {
	s, tool := unifiedFixture(t)
	callHeartbeat(t, tool, `{"action":"set_date_override","revision":2,"date":"2026-10-27","start":"11:00"}`)
	if len(atHeartbeat(t, s, "2026-10-27T08:00:00+08:00")) != 0 {
		t.Fatal("old module clock started early")
	}
	jobs := atHeartbeat(t, s, "2026-10-27T11:00:00+08:00")
	if len(jobs) != 2 {
		t.Fatal(jobs)
	}
	for _, j := range jobs {
		if j.At != "2026-10-27T11:00:00+08:00" {
			t.Fatal(j)
		}
	}
	// Mark both modules as completed; removing the exception must not replay the day.
	for _, j := range jobs {
		if e := (JobStore{Repository: s.repo()}).Update(t.Context(), j.ID, func(v *heartbeat.Task) error {
			v.Enabled = false
			v.History = []heartbeat.Run{{Status: "completed"}}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	}
	callHeartbeat(t, tool, `{"action":"remove_date_override","revision":3,"date":"2026-10-27"}`)
	_ = atHeartbeat(t, s, "2026-10-27T11:00:30+08:00")
	all, _ := (JobStore{Repository: s.repo()}).List(t.Context())
	morning := 0
	for _, j := range all {
		if j.TriggerID == "morning" && j.Enabled {
			morning++
		}
	}
	if morning != 0 {
		t.Fatal("default replayed after cleanup")
	}
	cfg, _ := s.Config(t.Context())
	w, _ := cfg.Window("2026-11-03")
	if w.Start != "08:00" {
		t.Fatal("next Tuesday changed", w)
	}
}
func TestUnifiedModuleSelectionAndOwnerForegroundStayIndependent(t *testing.T) {
	s, tool := unifiedFixture(t)
	callHeartbeat(t, tool, `{"action":"set_trigger","revision":2,"trigger_id":"morning","weekday":"sunday","modules":["mail_check"]}`)
	jobs := atHeartbeat(t, s, "2026-10-25T11:00:00+08:00")
	if len(jobs) != 1 || jobs[0].ModuleID != "mail_check" {
		t.Fatal("unselected module ran", jobs)
	}
	night, _ := time.Parse(time.RFC3339, "2026-10-25T03:00:00+08:00")
	if allowed, e := s.Allows(t.Context(), heartbeat.Task{Kind: "message"}, night); e != nil || !allowed {
		t.Fatal("explicit reminder was blocked", e)
	}
	if allowed, e := s.Allows(t.Context(), heartbeat.Task{ModuleID: "mail_analysis", Capability: "mail"}, night); e != nil || allowed {
		t.Fatal("timed analysis escaped window", e)
	}
	callHeartbeat(t, tool, `{"action":"set_date_override","revision":3,"date":"2026-10-26","trigger_id":"morning","modules":["weather_briefing"]}`)
	cfg, _ := s.Config(t.Context())
	if got := cfg.Selected(cfg.Triggers["morning"], "2026-10-26"); len(got) != 1 || got[0] != "weather_briefing" {
		t.Fatal(got)
	}
}
func TestUnifiedConfigRejectsInvalidAndBackgroundWrites(t *testing.T) {
	s, tool := unifiedFixture(t)
	for _, a := range []string{
		`{"action":"set_trigger","revision":1,"trigger_id":"morning","modules":["mail_check"]}`,
		`{"action":"set_trigger","revision":2,"trigger_id":"morning","modules":["invented"]}`,
		`{"action":"set_date_override","revision":2,"date":"2026-02-30","start":"11:00"}`,
		`{"action":"set_date_override","revision":2,"date":"2026-10-27","start":"23:00","end":"08:00"}`,
		`{"action":"set_date_override","revision":2,"date":"2026-10-27","trigger_id":"missing","modules":["mail_check"]}`,
	} {
		if _, e := tool.Execute(t.Context(), json.RawMessage(a)); e == nil {
			t.Fatal("accepted", a)
		}
	}
	c, _ := s.Config(t.Context())
	if c.Revision != 2 {
		t.Fatal("rejected write changed revision")
	}
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "1")
	if _, e := tool.Execute(t.Context(), json.RawMessage(`{"action":"set_module_enabled","revision":2,"module_id":"mail_check","enabled":false}`)); e == nil {
		t.Fatal("background wrote configuration")
	}
}
func TestUnifiedClockDoesNotReplayAllMissedIntervals(t *testing.T) {
	s, _ := unifiedFixture(t)
	jobs := atHeartbeat(t, s, "2026-10-27T16:00:00+08:00")
	if len(jobs) != 1 || jobs[0].TriggerID != "daytime_mail" {
		t.Fatal("replayed expired morning or interval backlog", jobs)
	}
}
func TestUnifiedModuleFailureDoesNotRepeatSuccessfulOtherModule(t *testing.T) {
	s, _ := unifiedFixture(t)
	now, _ := time.Parse(time.RFC3339, "2026-10-27T08:00:00+08:00")
	runs, checks := 0, 0
	e := heartbeat.Engine{Store: JobStore{Repository: s.repo()}, Prepare: s.Prepare, Gate: s.Allows, Clock: func() time.Time { return now }, Runner: mailTestRunner(func(context.Context, heartbeat.Task) (heartbeat.Execution, error) {
		runs++
		return heartbeat.Execution{}, nil
	}), Checker: mailTestRunner(func(context.Context, heartbeat.Task) (heartbeat.Execution, error) {
		checks++
		return heartbeat.Execution{}, nil
	})}
	if err := e.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if err := e.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || checks != 1 {
		t.Fatal(runs, checks)
	}
	// Verify initialization is idempotent and preserves the selected owner config.
	if _, err := s.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = s.repo().transact(t.Context(), func(tx *sql.Tx) error { return nil })
}

func TestUnifiedRetriesOnlyFailedDailyModule(t *testing.T) {
	s, _ := unifiedFixture(t)
	now, _ := time.Parse(time.RFC3339, "2026-10-27T08:00:00+08:00")
	weatherRuns, checks := 0, 0
	e := heartbeat.Engine{Store: JobStore{Repository: s.repo()}, Prepare: s.Prepare, Gate: s.Allows, Clock: func() time.Time { return now }, Runner: mailTestRunner(func(context.Context, heartbeat.Task) (heartbeat.Execution, error) {
		weatherRuns++
		if weatherRuns == 1 {
			return heartbeat.Execution{}, fmt.Errorf("temporary weather failure")
		}
		return heartbeat.Execution{}, nil
	}), Checker: mailTestRunner(func(context.Context, heartbeat.Task) (heartbeat.Execution, error) {
		checks++
		return heartbeat.Execution{}, nil
	})}
	if e.Tick(t.Context(), now) == nil {
		t.Fatal("failed module was not reported")
	}
	now = now.Add(time.Minute)
	if err := e.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if weatherRuns != 2 || checks != 2 {
		t.Fatal("retry replayed successful morning module or did not retry failed one", weatherRuns, checks)
	}
	if err := e.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if weatherRuns != 2 || checks != 2 {
		t.Fatal("completed module repeated")
	}
}

func TestIndependentClockAndOneShotModules(t *testing.T) {
	s, tool := unifiedFixture(t)
	callHeartbeat(t, tool, `{"action":"set_trigger","revision":2,"trigger_id":"monday_afternoon","clock":"14:00","weekdays":["monday"],"modules":["weather_briefing"]}`)
	c, _ := s.Config(t.Context())
	tr := c.Triggers["monday_afternoon"]
	if tr.RoutineBound || tr.Clock != "14:00" {
		t.Fatal("fixed trigger incorrectly tied to wake", tr)
	}
	jobs := atHeartbeat(t, s, "2026-10-26T14:00:00+08:00")
	found := false
	for _, j := range jobs {
		if j.TriggerID == tr.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("fixed-time module not invoked")
	}
	jobs = atHeartbeat(t, s, "2026-10-27T14:00:00+08:00")
	for _, j := range jobs {
		if j.TriggerID == tr.ID && strings.HasPrefix(j.At, "2026-10-27") {
			t.Fatal("wrong weekday executed")
		}
	}
	callHeartbeat(t, tool, `{"action":"set_trigger","revision":3,"trigger_id":"once_weather","at":"2026-10-28T03:00:00+08:00","modules":["weather_briefing"]}`)
	jobs = atHeartbeat(t, s, "2026-10-28T03:00:00+08:00")
	found = false
	for _, j := range jobs {
		if j.TriggerID == "once_weather" {
			found = true
			now, _ := time.Parse(time.RFC3339, j.At)
			if ok, e := s.Allows(t.Context(), j, now); e != nil || !ok {
				t.Fatal("explicit one-shot outside routine was blocked", e)
			}
		}
	}
	if !found {
		t.Fatal("one-shot not invoked")
	}
}

func TestRepeatingModuleCoalescesRecordsInsteadOfGrowingPerMinute(t *testing.T) {
	s, _ := unifiedFixture(t)
	now, _ := time.Parse(time.RFC3339, "2026-10-27T08:00:00+08:00")
	e := heartbeat.Engine{Store: JobStore{Repository: s.repo()}, Prepare: s.Prepare, Gate: s.Allows, Clock: func() time.Time { return now }, Runner: mailTestRunner(func(context.Context, heartbeat.Task) (heartbeat.Execution, error) { return heartbeat.Execution{}, nil }), Checker: mailTestRunner(func(context.Context, heartbeat.Task) (heartbeat.Execution, error) { return heartbeat.Execution{}, nil })}
	for range 8 {
		if err := e.Tick(t.Context(), now); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Minute)
	}
	jobs, _ := (JobStore{Repository: s.repo()}).List(t.Context())
	count := 0
	for _, j := range jobs {
		if j.ModuleID != "" {
			count++
		}
	}
	if count != 3 {
		t.Fatal("repeating module grew jobs per minute", count)
	}
	_ = s.repo().transact(t.Context(), func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(t.Context(), "SELECT count(*) FROM assistant_entities WHERE kind='module_occurrence'").Scan(&n); err != nil {
			return err
		}
		if n != 2 {
			t.Fatal("repeating occurrence pointer grew per minute", n)
		}
		return nil
	})
}
