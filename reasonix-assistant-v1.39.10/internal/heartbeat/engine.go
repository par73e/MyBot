package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"reasonix/internal/identitylock"
)

// Execution is the final answer and optional resumable Agent session.
type Execution struct {
	DecisionReason string
	Text           string
	SessionID      string
}

// Runner executes an Agent task; message reminders bypass it entirely.
type Runner interface {
	Run(context.Context, Task) (Execution, error)
}

// Sender delivers to one task's target without choosing recipients implicitly.
type Sender interface {
	Send(context.Context, Target, string) error
}

// ErrDeliveryUncertain prevents automatic replay after a partial or ambiguous send.
var ErrDeliveryUncertain = errors.New("notification may already have been delivered")

// Engine dispatches serially, retaining its cross-process lease through delivery.
type Engine struct {
	Store      TaskStore
	Runner     Runner
	Sender     Sender
	Clock      func() time.Time
	kindFilter string
}

// Start checks immediately and then polls until cancellation. Store errors are
// reported without overwriting the file, so a corrected file can be reloaded.
func (e Engine) Start(ctx context.Context, interval time.Duration, report func(error)) error {
	if interval < time.Second {
		return errors.New("poll interval must be at least 1s")
	}
	if e.Clock == nil {
		e.Clock = time.Now
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := e.Tick(ctx, time.Now()); err != nil && report != nil {
			report(err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Tick dispatches due work once. Competing workers leave execution to the owner.
func (e Engine) Tick(ctx context.Context, now time.Time) error {
	if e.kindFilter != "" {
		return e.withLease(func() error { return e.dispatch(ctx, now, "") })
	}
	var failures []error
	for _, kind := range []string{"message", "agent"} {
		lane := e
		lane.kindFilter = kind
		failures = append(failures, lane.Tick(ctx, now))
	}
	return errors.Join(failures...)
}

// Trigger executes a task manually, including a previously attempted one-shot.
func (e Engine) Trigger(ctx context.Context, id string, now time.Time) error {
	tasks, err := e.Store.List(ctx)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.ID == id {
			e.kindFilter = task.Kind
			return e.withLease(func() error { return e.dispatch(ctx, now, id) })
		}
	}
	return fmt.Errorf("heartbeat task %q not found", id)
}

func (e Engine) withLease(action func() error) error {
	if err := os.MkdirAll(filepath.Dir(e.Store.LeasePath()), 0o700); err != nil {
		return err
	}
	release, err := identitylock.TryAcquire(e.Store.LeasePath() + "." + e.kindFilter)
	if errors.Is(err, identitylock.ErrHeld) {
		return errors.New("heartbeat worker already executing")
	}
	if err != nil {
		return err
	}
	defer release()
	return action()
}

func (e Engine) dispatch(ctx context.Context, now time.Time, manualID string) error {
	tasks, err := e.Store.List(ctx)
	if err != nil {
		return err
	}
	var failures []error
	found := manualID == ""
	for _, task := range tasks {
		if e.kindFilter != "" && task.Kind != e.kindFilter {
			continue
		}
		if manualID != "" && task.ID != manualID {
			continue
		}
		found = true
		if ctx.Err() != nil {
			return ctx.Err()
		}
		currentTime := now
		if e.Clock != nil {
			currentTime = e.Clock()
		}
		if err := e.dispatchTask(ctx, task, currentTime, manualID != ""); err != nil {
			failures = append(failures, fmt.Errorf("task %s: %w", task.ID, err))
		}
	}
	if !found {
		return fmt.Errorf("heartbeat task %q not found", manualID)
	}
	return errors.Join(failures...)
}

func (e Engine) dispatchTask(ctx context.Context, task Task, now time.Time, manual bool) error {
	last := lastRun(&task)
	if last != nil && last.Status == "running" {
		return e.updateRun(task.ID, func(t *Task, r *Run) {
			r.Status, r.Error, r.EndedAt = "interrupted", "previous worker exited during execution; inspect before retrying", now.UnixMilli()
			if t.At != "" {
				t.Enabled = false
			}
		})
	}
	if last != nil && last.Status == "delivery_pending" {
		return e.deliver(ctx, task.ID, now)
	}
	if !manual && !task.Due(now) {
		return nil
	}
	if task.Expired(now) {
		return errors.New("task has expired")
	}
	if err := e.claim(ctx, &task, now, manual); err != nil {
		return err
	}
	out := Execution{Text: task.Prompt}
	var err error
	if task.Kind == "agent" {
		// A removed workspace must not block management or unrelated jobs.
		err = task.validateWorkspace()
		if err == nil {
			if e.Runner == nil {
				err = errors.New("Agent runner is unavailable")
			} else {
				out, err = e.Runner.Run(ctx, task)
			}
		}
	}
	if saveErr := e.finish(task.ID, out, err); saveErr != nil {
		return saveErr
	}
	if err != nil {
		return err
	}
	return e.deliver(ctx, task.ID, now)
}

func (e Engine) claim(ctx context.Context, task *Task, now time.Time, manual bool) error {
	return e.Store.Update(ctx, task.ID, func(current *Task) error {
		if (!manual && !current.Due(now)) || current.Expired(now) {
			return errors.New("schedule changed before execution")
		}
		*task = *current
		current.LastRunAt = now.UnixMilli()
		current.SnoozedUntil = ""
		current.History = append(current.History, Run{StartedAt: now.UnixMilli(), Status: "running"})
		if len(current.History) > 20 {
			current.History = current.History[len(current.History)-20:]
		}
		return nil
	})
}

func (e Engine) finish(id string, out Execution, runErr error) error {
	return e.updateRun(id, func(t *Task, r *Run) {
		r.Result, r.SessionID, r.EndedAt = out.Text, out.SessionID, time.Now().UnixMilli()
		r.DecisionReason = out.DecisionReason
		r.Status = "completed"
		if runErr != nil {
			r.Status, r.Error = "failed", runErr.Error()
		}
		if skipped(*t, time.Now()) {
			r.Status = "skipped"
			return
		}
		if runErr == nil && out.Text != "" && t.Target != nil {
			r.Status = "delivery_pending"
		}
		if t.At != "" && r.Status != "delivery_pending" {
			t.Enabled = false
		}
	})
}

func (e Engine) deliver(ctx context.Context, id string, now time.Time) error {
	if e.Clock != nil {
		now = e.Clock()
	}
	tasks, err := e.Store.List(ctx)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.ID != id {
			continue
		}
		r := lastRun(&task)
		if r == nil || r.Status != "delivery_pending" || !task.Enabled || skipped(task, time.Now()) {
			return nil
		}
		if r.NextDeliveryAt > now.UnixMilli() {
			return nil
		}
		if task.Target == nil {
			return errors.New("pending notification has no target")
		}
		if task.Expired(time.Now()) {
			return e.updateRun(id, func(t *Task, r *Run) { r.Status = "delivery_expired"; t.Enabled = false })
		}
		if e.Sender == nil {
			return errors.New("notification sender unavailable; result retained for delivery")
		}
		if err := e.Sender.Send(ctx, *task.Target, r.Result); err != nil {
			_ = e.updateRun(id, func(t *Task, r *Run) { recordDeliveryFailure(t, r, err, now) })
			return err
		}
		return e.updateRun(id, func(t *Task, r *Run) {
			r.Status, r.Error = "delivered", ""
			r.NextDeliveryAt = 0
			if t.At != "" {
				t.Enabled = false
			}
		})
	}
	return nil
}

func (e Engine) updateRun(id string, edit func(*Task, *Run)) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return e.Store.Update(ctx, id, func(t *Task) error {
		if r := lastRun(t); r != nil {
			edit(t, r)
		}
		return nil
	})
}

func lastRun(t *Task) *Run {
	if len(t.History) == 0 {
		return nil
	}
	return &t.History[len(t.History)-1]
}

func recordDeliveryFailure(t *Task, r *Run, err error, now time.Time) {
	r.DeliveryAttempts = min(8, max(1, r.DeliveryAttempts+1))
	r.NextDeliveryAt = now.Add(min(10*time.Second*time.Duration(1<<min(r.DeliveryAttempts-1, 5)), 5*time.Minute)).UnixMilli()
	r.Error = err.Error()
	if errors.Is(err, ErrDeliveryUncertain) {
		r.Status = "delivery_uncertain"
		r.NextDeliveryAt = 0
	} else if r.DeliveryAttempts >= 8 {
		r.Status = "delivery_failed"
		if t.At != "" {
			t.Enabled = false
		}
	}
}
