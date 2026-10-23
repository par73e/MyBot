package heartbeat

import (
	"context"
	"errors"
	"time"
)

// ErrDeferred leaves work pending without charging a failure or delivery attempt.
var ErrDeferred = errors.New("heartbeat execution deferred by schedule policy")

// Admitted checks the latest policy at execution and again at delivery.
func (e Engine) Admitted(ctx context.Context, task Task, now time.Time) (bool, error) {
	if e.Gate == nil {
		return true, nil
	}
	return e.Gate(ctx, task, now)
}

func (e Engine) runAdmitted(ctx context.Context, task Task) (Execution, error) {
	runner := e.Runner
	if task.Kind == "mail_check" || task.Kind == "weather_check" {
		runner = e.Checker
	}
	if runner == nil {
		return Execution{}, errors.New("heartbeat runner unavailable for task kind")
	}
	if e.Gate == nil || (task.Kind != "mail_check" && task.Kind != "weather_check" && task.Capability != "mail" && task.Capability != "weather") {
		return runner.Run(ctx, task)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				if e.Clock != nil {
					now = e.Clock()
				}
				allowed, err := e.Admitted(runCtx, task, now)
				if err != nil || !allowed {
					cancel()
					return
				}
			}
		}
	}()
	out, err := runner.Run(runCtx, task)
	stopped := runCtx.Err() != nil && ctx.Err() == nil
	cancel()
	<-done
	if stopped {
		return Execution{}, ErrDeferred
	}
	return out, err
}
