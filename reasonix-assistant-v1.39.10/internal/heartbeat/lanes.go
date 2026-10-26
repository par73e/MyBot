package heartbeat

import (
	"context"
	"errors"
	"sync"
	"time"
)

// StartLanes has one scheduling clock. Independent bounded workers prevent slow
// module calls from blocking the clock or the fixed-message lane. Signals
// coalesce while busy; they are not an inbox or a backlog of missed ticks.
func (e Engine) StartLanes(ctx context.Context, interval time.Duration, report func(error)) error {
	if interval < time.Second {
		return errors.New("poll interval must be at least 1s")
	}
	if e.Clock == nil {
		e.Clock = time.Now
	}
	var wg sync.WaitGroup
	signals := []chan time.Time{}
	for _, kind := range []string{"message", "agent", "mail_check", "weather_check", "module_check"} {
		lane := e
		lane.kindFilter = kind
		lane.Prepare = nil
		ch := make(chan time.Time, 1)
		signals = append(signals, ch)
		wg.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case now := <-ch:
					if err := lane.Tick(ctx, now); err != nil && report != nil {
						report(err)
					}
				}
			}
		})
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		now := time.Now()
		if e.Clock != nil {
			now = e.Clock()
		}
		if e.Prepare != nil {
			if err := e.Prepare(ctx, now); err != nil && report != nil {
				report(err)
			}
		}
		for _, ch := range signals {
			select {
			case ch <- now:
			default:
			}
		}
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil
		case <-ticker.C:
		}
	}
}
func skipped(t Task, now time.Time) bool {
	end, err := time.Parse(time.RFC3339, t.SkipUntil)
	return err == nil && now.Before(end)
}
