package assistant

import (
	"reasonix/internal/heartbeat"
	"testing"
	"time"
)

func TestNotifiedReminderViewOnlyConfirmedCurrentOneShot(t *testing.T) {
	todo := Todo{Kind: "reminder", Status: "open"}
	job := heartbeat.Task{At: "2026-10-29T13:00:00+08:00", History: []heartbeat.Run{{Status: "delivered", ScheduledAt: "2026-10-29T13:00:00+08:00"}}}
	if got := notifiedReminderView(todo, &job).Status; got != "notified" {
		t.Fatal(got)
	}
	for _, state := range []string{"delivery_pending", "delivery_uncertain", "delivery_failed", "skipped"} {
		job.History[0].Status = state
		if got := notifiedReminderView(todo, &job).Status; got != "open" {
			t.Fatal(state, got)
		}
	}
	job.History[0].Status = "delivered"
	job.Enabled = true
	if notifiedReminderView(todo, &job).Status != "open" {
		t.Fatal("enabled future job hidden")
	}
	job.Enabled = false
	job.History[0].ScheduledAt = "2026-10-28T13:00:00+08:00"
	if notifiedReminderView(todo, &job).Status != "open" {
		t.Fatal("old delivery used")
	}
	job.History[0].ScheduledAt = job.At
	job.Interval = "24h"
	if notifiedReminderView(todo, &job).Status != "open" {
		t.Fatal("recurrence hidden")
	}
	job.Interval = ""
	todo.Kind = "todo"
	if notifiedReminderView(todo, &job).Status != "open" {
		t.Fatal("todo hidden")
	}
}
func TestDeliveredReminderExcludedFromActiveListAndEvening(t *testing.T) {
	h, tasks, _ := expansionFixture(t)
	v := createTask(t, tasks, TaskRequest{Title: "单次提醒", Kind: "reminder", Body: "提醒", At: "2026-10-25T13:00:00+08:00"})
	store := JobStore{Repository: h.repo()}
	if e := store.Update(t.Context(), v.JobID, func(j *heartbeat.Task) error {
		j.Enabled = false
		j.History = []heartbeat.Run{{Status: "delivered", ScheduledAt: j.At}}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	active, e := tasks.List(t.Context(), false)
	if e != nil || len(active) != 0 {
		t.Fatal(active, e)
	}
	all, e := tasks.List(t.Context(), true)
	if e != nil || len(all) != 1 || all[0].Status != "notified" {
		t.Fatal(all, e)
	}
	svc := EveningService{Home: h.Home, Clock: func() time.Time { return timeMust("2026-10-25T23:00:00+08:00") }}
	snap, e := svc.facts(t.Context(), "test")
	if e != nil || len(snap.Tasks) != 0 {
		t.Fatal(snap.Tasks, e)
	}
}

func TestFutureReminderNotPresentedAsUnfinished(t *testing.T) {
	h, tasks, _ := expansionFixture(t)
	createTask(t, tasks, TaskRequest{Title: "晚间提醒", Kind: "reminder", Body: "提醒", At: "2026-10-25T23:15:00+08:00"})
	svc := EveningService{Home: h.Home, Clock: func() time.Time { return timeMust("2026-10-25T23:00:00+08:00") }}
	snap, e := svc.facts(t.Context(), "test")
	if e != nil || len(snap.Tasks) != 0 {
		t.Fatal(snap.Tasks, e)
	}
	active, e := tasks.List(t.Context(), false)
	if e != nil || len(active) != 1 || active[0].Status != "open" {
		t.Fatal(active, e)
	}
}
