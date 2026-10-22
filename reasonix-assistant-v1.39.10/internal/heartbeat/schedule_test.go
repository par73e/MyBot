package heartbeat

import (
	"testing"
	"time"
)

func TestCalendarRulesTimezoneAndExpiry(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	monday := time.Date(2026, 10, 19, 8, 0, 0, 0, loc)
	for _, rule := range []string{"0 8 * * 1-5", "24h|daily@08:00", "168h|weekly:mon,wed@08:00"} {
		t.Run(rule, func(t *testing.T) {
			task := Task{Enabled: true, Interval: rule, Timezone: "Asia/Shanghai", CreatedAt: monday.Add(-time.Minute).UnixMilli()}
			if task.Due(monday.Add(-time.Second)) {
				t.Fatal("early dispatch")
			}
			if !task.Due(monday.UTC()) {
				t.Fatal("did not use task timezone")
			}
			task.LastRunAt = monday.UnixMilli()
			if task.Due(monday.Add(15 * time.Second)) {
				t.Fatal("duplicate occurrence")
			}
			task.LastRunAt = 0
			task.EndsAt = monday.Format(time.RFC3339)
			if task.Due(monday) {
				t.Fatal("expiry boundary is not exclusive")
			}
		})
	}
}
func TestIntervalOnlyRunsWithinWindow(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	task := Task{Enabled: true, Interval: "15m", Timezone: "Asia/Shanghai", TimeWindowStart: "08:00", TimeWindowEnd: "22:00", CreatedAt: time.Date(2026, 10, 22, 7, 0, 0, 0, loc).UnixMilli()}
	if task.Due(time.Date(2026, 10, 22, 7, 59, 0, 0, loc)) {
		t.Fatal("ran outside window")
	}
	if !task.Due(time.Date(2026, 10, 22, 8, 0, 0, 0, loc)) {
		t.Fatal("missed window start")
	}
	if task.Due(time.Date(2026, 10, 22, 22, 0, 0, 0, loc)) {
		t.Fatal("ran at excluded window end")
	}
}
func TestRejectInvalidTask(t *testing.T) {
	_, base, _ := fixture(t)
	for _, change := range []func(*Task){
		func(t *Task) { t.Interval = "5m" }, func(t *Task) { t.At = ""; t.Interval = "99 * * * *" },
		func(t *Task) { t.Timezone = "Mars/Unknown" }, func(t *Task) { t.At = "tomorrow" },
		func(t *Task) { t.Kind = "shell" }, func(t *Task) { t.ApprovalMode = "danger-full-access" },
		func(t *Task) { t.Target = &Target{ChatID: "missing-connection"} },
	} {
		task := base
		change(&task)
		if task.Validate() == nil {
			t.Fatalf("accepted invalid task: %+v", task)
		}
	}
}
