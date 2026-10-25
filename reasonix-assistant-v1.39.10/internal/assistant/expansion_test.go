package assistant

import (
	"context"
	"errors"
	"reasonix/internal/heartbeat"
	"strings"
	"testing"
	"time"
)

func expansionFixture(t *testing.T) (HeartbeatService, Tasks, time.Time) {
	t.Helper()
	h, _ := unifiedFixture(t)
	if _, e := h.EnableExpansion(t.Context()); e != nil {
		t.Fatal(e)
	}
	now := timeMust("2026-10-25T11:00:00+08:00")
	return h, Tasks{Repository: h.repo(), Options: h.Options, Now: func() time.Time { return now }}, now
}
func TestExpansionRoutineAndEveningClock(t *testing.T) {
	h, _, _ := expansionFixture(t)
	c, e := h.Config(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	for _, day := range []string{"monday", "wednesday", "thursday"} {
		if c.Weekly[day].Start != "09:50" {
			t.Fatal(c.Weekly)
		}
	}
	for _, w := range c.Weekly {
		if w.End != "23:30" {
			t.Fatal(w)
		}
	}
	for _, at := range []string{"2026-10-26T09:49:59+08:00", "2026-10-26T23:30:00+08:00"} {
		ok, e := h.Allows(t.Context(), heartbeat.Task{ID: "generic", At: at}, timeMust(at))
		if ok || e != nil && !errors.Is(e, heartbeat.ErrOutsideRoutine) {
			t.Fatal(at, ok, e)
		}
	}
	for _, at := range []string{"2026-10-26T09:50:00+08:00", "2026-10-26T23:29:59+08:00"} {
		ok, e := h.Allows(t.Context(), heartbeat.Task{ID: "generic", At: at}, timeMust(at))
		if !ok || e != nil {
			t.Fatal(at, ok, e)
		}
	}
	var evening heartbeat.Task
	for _, j := range atHeartbeat(t, h, "2026-10-25T23:00:00+08:00") {
		if j.ModuleID == "evening_briefing" {
			evening = j
		}
	}
	if evening.At != "2026-10-25T23:00:00+08:00" || evening.Capability != "evening" || evening.EndsAt != "2026-10-25T23:10:00+08:00" {
		t.Fatal(evening)
	}
}
func TestDeadlinePlanCancelAndDelayRejectStaleDelivery(t *testing.T) {
	h, s, now := expansionFixture(t)
	yes := true
	v := createTask(t, s, TaskRequest{Title: "提交申请材料", Kind: "todo", DueAt: "2026-10-27T18:00:00+08:00", DeadlineReminders: &yes})
	if len(v.Reminders) != 2 || v.Reminders[0].At != "2026-10-26T18:00:00+08:00" || v.Reminders[1].At != "2026-10-27T16:00:00+08:00" {
		t.Fatal(v)
	}
	old := v.Reminders[0]
	out, e := s.Apply(t.Context(), TaskRequest{Action: "update", ID: v.ID, Revision: v.Revision, DueAt: "2026-10-28T19:00:00+08:00"})
	if e != nil {
		t.Fatal(e)
	}
	v = out.(TaskView)
	if len(v.Reminders) != 2 {
		t.Fatal(v)
	}
	if ok, e := h.Allows(t.Context(), old, timeMust(old.At)); ok || !errors.Is(e, heartbeat.ErrOccurrenceChanged) {
		t.Fatal("stale delayed reminder admitted", ok, e)
	}
	if _, e = s.Apply(t.Context(), TaskRequest{Action: "complete", ID: v.ID, Revision: v.Revision}); e != nil {
		t.Fatal(e)
	}
	for _, j := range v.Reminders {
		if ok, e := h.Allows(t.Context(), j, timeMust(j.At)); ok || !errors.Is(e, heartbeat.ErrOccurrenceChanged) {
			t.Fatal(ok, e)
		}
	}
	all, e := s.List(t.Context(), true)
	if e != nil || all[0].Status != "done" {
		t.Fatal(all, e)
	}
	_ = now
}
func TestDeadlineWindowWarningsAndNoExtraOverduePush(t *testing.T) {
	h, s, _ := expansionFixture(t)
	yes := true
	v := createTask(t, s, TaskRequest{Title: "清晨到期事项", Kind: "todo", DueAt: "2026-10-27T09:00:00+08:00", DeadlineReminders: &yes})
	if len(v.Reminders) != 0 || len(v.ReminderWarnings) != 2 {
		t.Fatal(v)
	}
	if e := h.Prepare(t.Context(), timeMust("2026-10-28T12:00:00+08:00")); e != nil {
		t.Fatal(e)
	}
	jobs, e := (JobStore{Repository: s.Repository}).List(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	for _, j := range jobs {
		if strings.HasPrefix(j.ID, "deadline-"+v.ID) {
			t.Fatal("unexpected overdue push", j)
		}
	}
}
func TestSubscriptionRelativeWakeRenewalAndDeliveryDedup(t *testing.T) {
	h, _, now := expansionFixture(t)
	svc := Subscriptions{Repository: h.repo()}
	out, e := svc.Apply(t.Context(), SubscriptionRequest{Action: "create", Name: "测试会员", ExpiresOn: "2026-11-01"})
	if e != nil {
		t.Fatal(e)
	}
	sub := out.(Subscription)
	var old heartbeat.Task
	for _, j := range atHeartbeat(t, h, now.Format(time.RFC3339)) {
		if j.ModuleID == "subscription_reminder" {
			old = j
		}
	}
	if old.At != "2026-10-25T11:00:00+08:00" {
		t.Fatal(old)
	}
	if ok, e := h.Allows(t.Context(), old, now); !ok || e != nil {
		t.Fatal(ok, e)
	}
	if _, e = svc.Apply(t.Context(), SubscriptionRequest{Action: "renew", ID: sub.ID, Revision: sub.Revision}); e == nil {
		t.Fatal("renewal guessed expiry")
	}
	out, e = svc.Apply(t.Context(), SubscriptionRequest{Action: "renew", ID: sub.ID, Revision: sub.Revision, ExpiresOn: "2026-11-02"})
	if e != nil {
		t.Fatal(e)
	}
	sub = out.(Subscription)
	if ok, e := h.Allows(t.Context(), old, now); ok || !errors.Is(e, heartbeat.ErrOccurrenceChanged) {
		t.Fatal(ok, e)
	}
	var next heartbeat.Task
	for _, j := range atHeartbeat(t, h, "2026-10-26T09:50:00+08:00") {
		if j.ModuleID == "subscription_reminder" && j.Revision == sub.Revision {
			next = j
		}
	}
	if next.At != "2026-10-26T09:50:00+08:00" {
		t.Fatal(next)
	}
	store := JobStore{Repository: h.repo()}
	if e = store.Update(t.Context(), next.ID, func(j *heartbeat.Task) error {
		j.Enabled = false
		j.History = []heartbeat.Run{{Status: "delivered"}}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	for _, j := range atHeartbeat(t, h, "2026-10-26T09:51:00+08:00") {
		if j.ID == next.ID {
			t.Fatal("delivered subscription reactivated")
		}
	}
}
func TestEveningRefreshesFactsAndKeepsMissingDistinct(t *testing.T) {
	h, s, _ := expansionFixture(t)
	importCourses(t, CourseService{Home: h.Home})
	v := createTask(t, s, TaskRequest{Title: "测试待办", Kind: "todo", PlannedFor: "2026-10-25"})
	now := timeMust("2026-10-25T23:00:00+08:00")
	svc := EveningService{Home: h.Home, Clock: func() time.Time { return now }}
	job := heartbeat.Task{ID: "evening-test", RunStartedAt: 101}
	if e := svc.Prepare(t.Context(), job); e != nil {
		t.Fatal(e)
	}
	t.Setenv("REASONIX_ASSISTANT_JOB_ID", job.ID)
	t.Setenv("REASONIX_ASSISTANT_EVENING_ATTEMPT", "101")
	t.Setenv("REASONIX_ASSISTANT_EVENING_BACKGROUND", "1")
	if _, e := (EveningTool{Service: svc}).Execute(t.Context(), []byte(`{"action":"snapshot"}`)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Apply(t.Context(), TaskRequest{Action: "complete", ID: v.ID, Revision: v.Revision}); e != nil {
		t.Fatal(e)
	}
	text, e := svc.Finalize(t.Context(), job, `{"care":"早点休息，晚安。"}`, false)
	if e != nil || strings.Contains(text, "测试待办") || !strings.Contains(text, "10:10") || !strings.Contains(text, "Room A") {
		t.Fatal(text, e)
	}
	fallback, e := svc.Finalize(context.Background(), job, `not json`, true)
	if e != nil || !strings.Contains(fallback, "晚安") {
		t.Fatal(fallback, e)
	}
}
