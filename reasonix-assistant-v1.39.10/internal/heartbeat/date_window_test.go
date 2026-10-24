package heartbeat

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestDateOverridePostponesOnlySelectedCivilDateAndSurvivesReload(t *testing.T) {
	_, task, _ := fixture(t)
	task.At, task.Interval, task.Kind = "", "60s", "mail_check"
	task.WeeklySchedule = requestedWeek()
	task.DateOverrides = DateOverrides{"2026-10-27": {Enabled: true, Start: "11:00", End: "24:00"}}
	if err := task.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(task)
	var restored Task
	if err := json.Unmarshal(raw, &restored); err != nil || !reflect.DeepEqual(restored.WeeklySchedule, requestedWeek()) {
		t.Fatal("reload changed base plan", err)
	}
	for _, tc := range []struct {
		at   string
		want bool
	}{
		{"2026-10-26T10:00:00+08:00", true},
		{"2026-10-27T00:00:00+08:00", false},
		{"2026-10-27T08:00:00+08:00", false},
		{"2026-10-27T10:59:59.999999999+08:00", false},
		{"2026-10-27T11:00:00+08:00", true},
		{"2026-10-27T23:59:59+08:00", true},
		{"2026-10-28T00:00:00+08:00", false},
		{"2026-10-28T10:00:00+08:00", true},
		{"2026-11-03T08:00:00+08:00", true},
	} {
		now, _ := time.Parse(time.RFC3339Nano, tc.at)
		if restored.WithinTimeWindow(now.UTC()) != tc.want || restored.Due(now.UTC()) != tc.want {
			t.Fatalf("wrong override/base boundary at %s; want %v", tc.at, tc.want)
		}
	}
}

func TestDateOverrideCanReplaceDailyOrDisabledWeeklyHoursWithoutResumingPause(t *testing.T) {
	_, task, _ := fixture(t)
	task.At, task.Interval, task.Kind, task.CreatedAt = "", "60s", "mail_check", 0
	task.TimeWindowStart, task.TimeWindowEnd = "12:00", "23:00"
	task.DateOverrides = DateOverrides{"2026-10-27": {Enabled: true, Start: "08:00", End: "24:00"}}
	now, _ := time.Parse(time.RFC3339, "2026-10-27T08:00:00+08:00")
	// LastRunAt exercises the ordinary interval path, not just first dispatch.
	task.LastRunAt = now.Add(-time.Hour).UnixMilli()
	if !task.Due(now) {
		t.Fatal("old daily window vetoed earlier override")
	}
	task.TimeWindowStart, task.TimeWindowEnd = "", ""
	task.WeeklySchedule = requestedWeek()
	task.WeeklySchedule["tuesday"] = DailyWindow{}
	if !task.Due(now) {
		t.Fatal("disabled weekday vetoed date override")
	}
	task.Enabled = false
	if task.Due(now) {
		t.Fatal("override resumed paused monitor")
	}
	task.Enabled = true
	task.DateOverrides["2026-10-27"] = DailyWindow{}
	if task.Due(now.Add(5 * time.Hour)) {
		t.Fatal("disabled override admitted work")
	}
}

func TestDateOverrideWinsOverPreviousOvernightWindowAndGatesCron(t *testing.T) {
	_, task, _ := fixture(t)
	task.At, task.Interval, task.Kind = "", "* * * * *", "agent"
	task.WeeklySchedule = requestedWeek()
	task.WeeklySchedule["monday"] = DailyWindow{Enabled: true, Start: "22:00", End: "08:00"}
	task.DateOverrides = DateOverrides{"2026-10-27": {Enabled: true, Start: "11:00", End: "24:00"}}
	now, _ := time.Parse(time.RFC3339, "2026-10-27T01:00:00+08:00")
	if task.WithinTimeWindow(now) || task.Due(now) {
		t.Fatal("previous night's weekly window escaped date override")
	}
}

func TestDateOverrideValidation(t *testing.T) {
	for _, raw := range []string{
		`{"2026-02-30":{"enabled":true,"start":"11:00","end":"24:00"}}`,
		`{"2026-1-27":{"enabled":true,"start":"11:00","end":"24:00"}}`,
		`{"2026-10-27":{"enabled":true,"start":"24:00","end":"24:00"}}`,
		`{"2026-10-27":{"enabled":true,"start":"11:00","end":"11:00"}}`,
		`{"2026-10-27":{"enabled":true,"start":"22:00","end":"08:00"}}`,
		`{"2026-10-27":{"enabled":true,"start":"11:00","end":"24:01"}}`,
		`{"2026-10-27":{"enabled":false,"start":"11:00","end":"24:00"}}`,
	} {
		var overrides DateOverrides
		if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
			t.Fatal(err)
		}
		if err := overrides.Validate(); err == nil {
			t.Fatal("accepted invalid override", raw)
		}
	}
}
