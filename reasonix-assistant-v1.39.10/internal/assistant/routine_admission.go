package assistant

import (
	"reasonix/internal/heartbeat"
	"time"
)

// Check the event against its civil-day routine, then the live execution window.
func routineAdmission(c HeartbeatConfig, t heartbeat.Task, now time.Time) (bool, error) {
	z := mustShanghai()
	event := t.ScheduledTime(now).In(z)
	date := event.Format("2006-01-02")
	start, end, ok := c.Bounds(date)
	if !ok || event.Before(start) || !event.Before(end) {
		return false, heartbeat.ErrOutsideRoutine
	}
	start, end, ok = c.Bounds(now.In(z).Format("2006-01-02"))
	return ok && !now.Before(start) && now.Before(end), nil
}
