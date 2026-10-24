package heartbeat

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestSingleClockContinuesWhileAgentLaneIsBusy(t *testing.T) {
	store, task, now := fixture(t)
	message := task
	message.ID = "independent-message"
	message.Kind = "message"
	if err := store.Add(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	runner := &testRunner{entered: make(chan struct{}), resume: make(chan struct{})}
	var clocks atomic.Int64
	e := Engine{Store: store, Runner: runner, Clock: func() time.Time { return now }, Prepare: func(context.Context, time.Time) error { clocks.Add(1); return nil }}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- e.StartLanes(ctx, time.Second, nil) }()
	select {
	case <-runner.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("agent lane did not start")
	}
	deadline := time.Now().Add(4 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		jobs, err := store.List(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, j := range jobs {
			if j.ID == message.ID && lastRun(&j) != nil && lastRun(&j).Status == "completed" {
				completed = true
			}
		}
		if completed && clocks.Load() >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !completed || clocks.Load() < 2 {
		t.Fatal("slow agent blocked independent message or master clock", completed, clocks.Load())
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("workers failed to stop")
	}
}
