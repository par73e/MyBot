package assistant

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"reasonix/internal/heartbeat"
)

func dateOverrideFixture(t *testing.T) (MailService, Options, MailScheduleTool) {
	t.Helper()
	s, o := mailFixture(t)
	if _, err := s.EnsureSchedule(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	now, _ := time.Parse(time.RFC3339, "2026-10-24T15:00:00+08:00")
	tool := MailScheduleTool{Service: s, Now: func() time.Time { return now }}
	week := heartbeat.WeeklySchedule{}
	for _, day := range weatherWeekdays {
		week[day] = heartbeat.DailyWindow{Enabled: true, Start: "08:00", End: "24:00"}
	}
	raw, _ := json.Marshal(MailScheduleRequest{Action: "update", Revision: 1, WeeklySchedule: &week})
	if _, err := tool.Execute(t.Context(), raw); err != nil {
		t.Fatal(err)
	}
	return s, o, tool
}

func TestMailDateOverrideOwnerSetMergeReloadRemoveAndDefaultRestoration(t *testing.T) {
	s, o, tool := dateOverrideFixture(t)
	before, _ := s.MailSchedule(t.Context())
	for _, args := range []string{
		`{"action":"set_date_override","revision":2,"date":"2026-10-27","date_window":{"enabled":true,"start":"11:00","end":"24:00"}}`,
		`{"action":"set_date_override","revision":3,"date":"2026-10-28","date_window":{"enabled":false}}`,
	} {
		if _, err := tool.Execute(t.Context(), json.RawMessage(args)); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := NewMailService(s.Home).EnsureSchedule(t.Context(), o)
	if err != nil || len(policy.DateOverrides) != 2 || !reflect.DeepEqual(policy.WeeklySchedule, before.WeeklySchedule) || policy.Interval != before.Interval || policy.Enabled != before.Enabled {
		t.Fatal("date merge/restart modified base settings", policy, err)
	}
	for _, tc := range []struct {
		at   string
		want bool
	}{
		{"2026-10-27T08:00:00+08:00", false},
		{"2026-10-27T10:59:59+08:00", false},
		{"2026-10-27T11:00:00+08:00", true},
		{"2026-10-28T12:00:00+08:00", false},
		{"2026-10-29T08:00:00+08:00", true},
		{"2026-11-03T08:00:00+08:00", true},
	} {
		now, _ := time.Parse(time.RFC3339, tc.at)
		for _, task := range []heartbeat.Task{policy, {Capability: "mail", PolicyID: MailScheduleID}} {
			if allowed, err := s.ScheduleAllows(t.Context(), task, now.UTC()); err != nil || allowed != tc.want {
				t.Fatal(tc.at, task.Kind, allowed, err)
			}
		}
		if allowed, err := s.ScheduleAllows(t.Context(), heartbeat.Task{Kind: "message"}, now); err != nil || !allowed {
			t.Fatal("mail override blocked an ordinary reminder", err)
		}
	}
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"action":"remove_date_override","revision":4,"date":"2026-10-27"}`)); err != nil {
		t.Fatal(err)
	}
	policy, _ = s.MailSchedule(t.Context())
	at, _ := time.Parse(time.RFC3339, "2026-10-27T08:00:00+08:00")
	if len(policy.DateOverrides) != 1 || !policy.WithinTimeWindow(at) || policy.DateOverrides["2026-10-28"].Enabled {
		t.Fatal("removal did not restore default or preserved other date incorrectly", policy)
	}
}

func TestMailDateOverrideRejectsAmbiguityStaleRevisionAndBackgroundMutation(t *testing.T) {
	s, _, tool := dateOverrideFixture(t)
	for _, args := range []string{
		`{"action":"set_date_override","revision":1,"date":"2026-10-27","date_window":{"enabled":false}}`,
		`{"action":"set_date_override","revision":2,"date":"2026-10-23","date_window":{"enabled":false}}`,
		`{"action":"set_date_override","revision":2,"date":"2026-02-30","date_window":{"enabled":false}}`,
		`{"action":"set_date_override","revision":2,"date":"2026-10-27","date_window":{}}`,
		`{"action":"set_date_override","revision":2,"date":"2026-10-27"}`,
		`{"action":"set_date_override","revision":2,"date":"2026-10-27","date_window":{"enabled":true,"start":"11:00","end":"08:00"}}`,
		`{"action":"set_date_override","revision":2,"date":"2026-10-27","date_window":{"enabled":true,"start":"11:00","end":"24:00"},"timeWindowStart":"11:00","timeWindowEnd":"00:00"}`,
		`{"action":"update","revision":2,"date":"2026-10-27","date_window":{"enabled":false}}`,
		`{"action":"pause","revision":2,"date":"2026-10-27"}`,
		`{"action":"remove_date_override","revision":2,"date":"2026-10-27","date_window":{"enabled":false}}`,
		`{"action":"set_date_override","revision":2,"date":"2026-10-27","date_window":{"enabled":false,"unexpected":true}}`,
	} {
		if _, err := tool.Execute(t.Context(), json.RawMessage(args)); err == nil {
			t.Fatal("accepted invalid override mutation", args)
		}
	}
	policy, _ := s.MailSchedule(t.Context())
	if policy.Revision != 2 || len(policy.DateOverrides) != 0 {
		t.Fatal("rejected call changed state", policy)
	}
	for _, mode := range []string{"REASONIX_ASSISTANT_BACKGROUND", "REASONIX_ASSISTANT_MAIL_BACKGROUND"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(mode, "1")
			if _, err := tool.Execute(t.Context(), json.RawMessage(`{"action":"set_date_override","revision":2,"date":"2026-10-27","date_window":{"enabled":false}}`)); err == nil {
				t.Fatal("background changed date policy")
			}
		})
	}
}

func TestMailDateOverrideDefersNativeCheckAndPendingDeliveryUntilResumeTime(t *testing.T) {
	s, o, tool := dateOverrideFixture(t)
	now, _ := time.Parse(time.RFC3339, "2026-10-27T08:00:00+08:00")
	checks, analyses, sends := 0, 0, 0
	s.Transport = func(_ctx context.Context, _ map[string]any) (mailWire, error) {
		checks++
		return mailWire{Results: []mailResult{goodResult("qq", MailMessage{TransportID: "1", Subject: "test"}), goodResult("gmail1"), goodResult("gmail2")}}, nil
	}
	e := heartbeat.Engine{Store: JobStore{Repository: s.Repository}, Gate: s.ScheduleAllows, Clock: func() time.Time { return now }, Checker: mailTestRunner(func(ctx context.Context, task heartbeat.Task) (heartbeat.Execution, error) {
		o.MailScheduleID = task.ID
		_, err := s.Poll(ctx, o)
		return heartbeat.Execution{}, err
	}), Runner: mailTestRunner(func(context.Context, heartbeat.Task) (heartbeat.Execution, error) {
		analyses++
		// The owner delays today's mail while an analysis is completing.
		_, err := tool.Execute(t.Context(), json.RawMessage(`{"action":"set_date_override","revision":2,"date":"2026-10-27","date_window":{"enabled":true,"start":"11:00","end":"24:00"}}`))
		return heartbeat.Execution{Text: "notice"}, err
	}), Sender: mailTestSender(func() { sends++ })}
	if err := e.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if err := e.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if checks != 1 || analyses != 1 || sends != 0 {
		t.Fatal("in-flight notice escaped newly added date override", checks, analyses, sends)
	}
	now, _ = time.Parse(time.RFC3339, "2026-10-27T10:59:59+08:00")
	if err := e.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if checks != 1 || sends != 0 {
		t.Fatal("mail ran during date quiet hours", checks, sends)
	}
	now = now.Add(time.Second)
	if err := e.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if sends != 1 || analyses != 1 {
		t.Fatal("resume lost notice or reran analysis", sends, analyses)
	}
}
