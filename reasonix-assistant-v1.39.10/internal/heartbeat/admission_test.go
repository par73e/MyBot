package heartbeat

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestMailGateCancelsInflightAndRetainsWorkForResume(t *testing.T) {
	store, task, now := fixture(t)
	if err := store.Update(t.Context(), task.ID, func(task *Task) error { task.Capability = "mail"; task.DecideNotification = true; return nil }); err != nil {
		t.Fatal(err)
	}
	var allowed atomic.Bool
	allowed.Store(true)
	entered := make(chan struct{})
	runner := &testRunner{entered: entered, resume: make(chan struct{})}
	e := Engine{Store: store, Runner: runner, Gate: func(context.Context, Task, time.Time) (bool, error) { return allowed.Load(), nil }}
	finished := make(chan error, 1)
	go func() { finished <- e.Tick(t.Context(), now) }()
	<-entered
	allowed.Store(false)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("running mail analysis ignored closed policy")
	}
	saved := onlyTask(t, store)
	if saved.LastRunAt != 0 || !saved.Enabled || lastRun(&saved).Status != "deferred" {
		t.Fatal("deferred work lost", saved)
	}
	allowed.Store(true)
	e.Runner = &testRunner{}
	if err := e.Tick(t.Context(), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	saved = onlyTask(t, store)
	if lastRun(&saved).Status != "completed" {
		t.Fatal("resume failed", saved)
	}
}

func TestDeferredDeliveryDoesNotConsumeAttempt(t *testing.T) {
	store, task, now := fixture(t)
	if err := store.Update(t.Context(), task.ID, func(task *Task) error { task.Target = &Target{ConnectionID: "weixin", ChatID: "owner"}; return nil }); err != nil {
		t.Fatal(err)
	}
	sender := &testSender{err: ErrDeferred}
	e := Engine{Store: store, Runner: &testRunner{result: Execution{Text: "notice"}}, Sender: sender}
	if err := e.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	saved := onlyTask(t, store)
	if lastRun(&saved).Status != "delivery_pending" || lastRun(&saved).DeliveryAttempts != 0 {
		t.Fatal("deferred delivery charged failure", saved)
	}
	sender.err = nil
	if err := e.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	saved = onlyTask(t, store)
	if lastRun(&saved).Status != "delivered" {
		t.Fatal(saved)
	}
}

func TestGlobalGateCancelsPlainAutomationWithoutModuleMetadata(t *testing.T) {
	store, _, now := fixture(t)
	var allowed atomic.Bool
	allowed.Store(true)
	entered := make(chan struct{})
	e := Engine{Store: store, Runner: &testRunner{entered: entered, resume: make(chan struct{})}, Gate: func(context.Context, Task, time.Time) (bool, error) { return allowed.Load(), nil }}
	done := make(chan error, 1)
	go func() { done <- e.Tick(t.Context(), now) }()
	<-entered
	allowed.Store(false)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("ordinary automation ignored the global gate")
	}
	if task := onlyTask(t, store); lastRun(&task).Status != "deferred" || task.LastRunAt != 0 {
		t.Fatal("plain automation was not cancelled safely", task)
	}
}
