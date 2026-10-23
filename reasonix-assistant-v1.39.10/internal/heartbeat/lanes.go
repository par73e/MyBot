package heartbeat

import (
	"context"
	"errors"
	"sync"
	"time"
)

// StartLanes isolates fixed reminders from slow Agent turns. Each lane keeps its
// own cross-process lease, including manual triggers, preventing double claims.
func (e Engine) StartLanes(ctx context.Context, interval time.Duration, report func(error)) error {
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for _, kind := range []string{"message", "agent", "mail_check", "weather_check"} {
		lane := e
		lane.kindFilter = kind
		wg.Go(func() { errs <- lane.Start(ctx, interval, report) })
	}
	wg.Wait()
	close(errs)
	var all []error
	for err := range errs {
		all = append(all, err)
	}
	return errors.Join(all...)
}

func skipped(t Task, now time.Time) bool {
	end, err := time.Parse(time.RFC3339, t.SkipUntil)
	return err == nil && now.Before(end)
}
