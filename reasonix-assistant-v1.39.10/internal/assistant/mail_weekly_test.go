package assistant

import (
	"encoding/json"
	"testing"
	"time"

	"reasonix/internal/heartbeat"
)

func TestMailWeeklyToolPersistsAndGatesExistingChildren(t *testing.T) {
	s, o := mailFixture(t)
	if _, err := s.EnsureSchedule(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	week := heartbeat.WeeklySchedule{}
	for _, day := range []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"} {
		start := "10:00"
		if day == "tuesday" || day == "friday" {
			start = "08:00"
		}
		if day == "saturday" || day == "sunday" {
			start = "11:00"
		}
		week[day] = heartbeat.DailyWindow{Enabled: true, Start: start, End: "24:00"}
	}
	tool := MailScheduleTool{Service: s}
	raw, _ := json.Marshal(MailScheduleRequest{Action: "update", Revision: 1, WeeklySchedule: &week})
	result, err := tool.Execute(t.Context(), raw)
	if err != nil {
		t.Fatal(result, err)
	}
	policy, err := NewMailService(s.Home).EnsureSchedule(t.Context(), o)
	if err != nil || len(policy.WeeklySchedule) != 7 || policy.TimeWindowStart != "" || policy.Revision != 2 {
		t.Fatal("restart lost weekly plan", policy, err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	child := heartbeat.Task{Capability: "mail", PolicyID: MailScheduleID}
	for _, tc := range []struct {
		day, hour int
		want      bool
	}{{26, 8, false}, {26, 10, true}, {27, 8, true}, {30, 8, true}, {31, 10, false}, {31, 11, true}} {
		now := time.Date(2026, 10, tc.day, tc.hour, 0, 0, 0, loc)
		if allowed, e := s.ScheduleAllows(t.Context(), child, now); e != nil || allowed != tc.want {
			t.Fatal(now, allowed, e)
		}
	}
	if _, err = tool.Execute(t.Context(), json.RawMessage(`{"action":"pause","revision":2}`)); err != nil {
		t.Fatal(err)
	}
	if allowed, _ := s.ScheduleAllows(t.Context(), child, time.Date(2026, 10, 27, 12, 0, 0, 0, loc)); allowed {
		t.Fatal("weekly plan bypassed pause")
	}
	if _, err = tool.Execute(t.Context(), json.RawMessage(`{"action":"update","revision":3,"timeWindowStart":"09:00","timeWindowEnd":"00:00"}`)); err != nil {
		t.Fatal(err)
	}
	policy, _ = s.MailSchedule(t.Context())
	if policy.WeeklySchedule != nil || policy.TimeWindowStart != "09:00" || policy.Enabled {
		t.Fatal("daily replacement or pause preservation failed", policy)
	}
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "1")
	if _, err = tool.Execute(t.Context(), raw); err == nil {
		t.Fatal("background changed weekly plan")
	}
}

func TestMailWeeklyToolRejectsAmbiguousUpdates(t *testing.T) {
	s, o := mailFixture(t)
	_, err := s.EnsureSchedule(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{
		`{"action":"update","revision":1,"weeklySchedule":{}}`,
		`{"action":"pause","revision":1,"clear_weekly_schedule":true}`,
		`{"action":"update","revision":1,"weeklySchedule":{},"timeWindowStart":"08:00","timeWindowEnd":"00:00"}`,
	} {
		if _, err = (MailScheduleTool{Service: s}).Execute(t.Context(), json.RawMessage(args)); err == nil {
			t.Fatal("accepted ambiguous update", args)
		}
	}
}
