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
	ConversationID string
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
	Prepare       func(context.Context, time.Time) error
	AfterDelivery func(context.Context, Task, Run) error
	Store         TaskStore
	Runner        Runner
	Checker       Runner
	Gate          func(context.Context, Task, time.Time) (bool, error)
	Sender        Sender
	Clock         func() time.Time
	kindFilter    string
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
	if e.Prepare != nil {
		failures = append(failures, e.Prepare(ctx, now))
	}
	for _, kind := range []string{"message", "agent", "mail_check", "weather_check"} {
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
	allowed, gateErr := e.Admitted(ctx, task, now)
	if errors.Is(gateErr, ErrOutsideRoutine) || errors.Is(gateErr, ErrOccurrenceChanged) {
		return e.skipOutsideRoutine(ctx, task, now, gateErr)
	}
	if gateErr != nil || !allowed {
		return gateErr
	}
	if task.Expired(now) {
		return errors.New("task has expired")
	}
	if err := e.claim(ctx, &task, now, manual); err != nil {
		return err
	}
	out := Execution{Text: task.MessageText(now)}
	var err error
	if task.Kind == "agent" || task.Kind == "mail_check" || task.Kind == "weather_check" {
		err = task.validateWorkspace()
		if err == nil {
			out, err = e.runAdmitted(ctx, task)
		}
	}
	if saveErr := e.finish(task.ID, out, err); saveErr != nil {
		return saveErr
	}
	if errors.Is(err, ErrDeferred) {
		return nil
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
		scheduledAt := current.ScheduledTime(now)
		*task = *current
		task.RunStartedAt = now.UnixMilli()
		current.LastRunAt = now.UnixMilli()
		current.SnoozedUntil = ""
		current.History = append(current.History, Run{StartedAt: now.UnixMilli(), Status: "running", ModuleID: current.ModuleID, TriggerRevision: current.TriggerRevision, ScheduledAt: scheduledAt.Format(time.RFC3339)})
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
		r.ConversationID = out.ConversationID
		r.Status = "completed"
		if errors.Is(runErr, ErrDeferred) {
			r.Status, r.Result = "deferred", ""
			t.LastRunAt = 0
			return
		}
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
		allowed, gateErr := e.Admitted(ctx, task, now)
		if errors.Is(gateErr, ErrOutsideRoutine) || errors.Is(gateErr, ErrOccurrenceChanged) {
			return e.updateRun(id, func(t *Task, r *Run) {
				r.Status, r.DecisionReason, r.NextDeliveryAt = "skipped", gateErr.Error(), 0
				if t.At != "" {
					t.Enabled = false
				}
			})
		}
		if gateErr != nil || !allowed {
			return gateErr
		}
		// Keep immutable outbox payloads across retries. A clock-bearing message
		// from a previous minute is skipped rather than reporting a false time.
		if staleReminderClock(task, *r, now) {
			return e.updateRun(id, func(t *Task, r *Run) {
				r.Status = "skipped"
				r.DecisionReason = "time-aware reminder wording is stale"
				r.NextDeliveryAt = 0
				if t.At != "" {
					t.Enabled = false
				}
			})
		}
		if e.Sender == nil {
			return errors.New("notification sender unavailable; result retained for delivery")
		}
		var sendErr error
		if sender, ok := e.Sender.(interface {
			SendRun(context.Context, Task, Run) error
		}); ok {
			sendErr = sender.SendRun(ctx, task, *r)
		} else {
			sendErr = e.Sender.Send(ctx, *task.Target, r.Result)
		}
		if errors.Is(sendErr, ErrOutsideRoutine) || errors.Is(sendErr, ErrOccurrenceChanged) {
			return e.updateRun(id, func(t *Task, r *Run) {
				r.Status = "skipped"
				r.DecisionReason = sendErr.Error()
				r.NextDeliveryAt = 0
				if t.At != "" {
					t.Enabled = false
				}
			})
		}
		if errors.Is(sendErr, ErrDeferred) {
			return nil
		}
		if err := sendErr; err != nil {
			_ = e.updateRun(id, func(t *Task, r *Run) { recordDeliveryFailure(t, r, err, now) })
			return err
		}
		if e.AfterDelivery != nil {
			if err := e.AfterDelivery(ctx, task, *r); err != nil {
				return err
			}
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

func (e Engine) skipOutsideRoutine(ctx context.Context, task Task, now time.Time, reason error) error {
	return e.Store.Update(ctx, task.ID, func(current *Task) error {
		if !current.Due(now) {
			return nil
		}
		at := current.ScheduledTime(now)
		current.LastRunAt = now.UnixMilli()
		current.History = append(current.History, Run{StartedAt: now.UnixMilli(), EndedAt: now.UnixMilli(), ScheduledAt: at.Format(time.RFC3339), Status: "skipped", DecisionReason: reason.Error(), ModuleID: current.ModuleID, TriggerRevision: current.TriggerRevision})
		if len(current.History) > 20 {
			current.History = current.History[len(current.History)-20:]
		}
		if current.At != "" {
			current.Enabled = false
		}
		return nil
	})
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

func staleReminderClock(task Task, r Run, now time.Time) bool {
	return task.Notification != nil && task.Notification.Mode == "time_aware" && r.Result != task.MessageText(now)
}
