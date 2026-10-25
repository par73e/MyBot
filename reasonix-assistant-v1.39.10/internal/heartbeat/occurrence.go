package heartbeat

import "time"

// ScheduledTime identifies the event, independently of when a worker is available.
func (t Task) ScheduledTime(now time.Time) time.Time {
	if n := len(t.History); n > 0 {
		r := t.History[n-1]
		if r.Status == "running" || r.Status == "delivery_pending" {
			if at, err := time.Parse(time.RFC3339, r.ScheduledAt); err == nil {
				return at
			}
			if r.StartedAt != 0 {
				return time.UnixMilli(r.StartedAt)
			}
		}
	}
	for _, value := range []string{t.SnoozedUntil, t.At} {
		if at, err := time.Parse(time.RFC3339, value); err == nil {
			return at
		}
	}
	loc, err := time.LoadLocation(t.Timezone)
	if err != nil {
		return now
	}
	if at, ok := previousHeartbeatScheduleAt(t, now.In(loc)); ok {
		return at
	}
	if isCronExpr(t.Interval) {
		return now.Truncate(time.Minute)
	}
	// Plain intervals coalesce at the latest elapsed position; missed ticks do not queue.
	if d, err := parseInterval(t.Interval); err == nil && d > 0 {
		base := t.LastRunAt
		if base == 0 {
			base = t.CreatedAt
		}
		if base != 0 {
			at := time.UnixMilli(base)
			if !now.Before(at) {
				return at.Add(now.Sub(at) / d * d)
			}
		}
	}
	return now
}

// ScheduledOn joins a still-open obligation to a civil day, independent of delivery status.
func (t Task) ScheduledOn(day time.Time) bool {
	loc, e := time.LoadLocation(t.Timezone)
	if e != nil {
		return false
	}
	day = day.In(loc)
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	inside := func(at time.Time) bool { return !at.Before(start) && at.Before(end) && !t.Expired(at) }
	if at, e := time.Parse(time.RFC3339, t.SnoozedUntil); e == nil {
		return inside(at)
	}
	for _, r := range t.History {
		if at, e := time.Parse(time.RFC3339, r.ScheduledAt); e == nil && inside(at) {
			return true
		}
	}
	if t.At != "" {
		at, e := time.Parse(time.RFC3339, t.At)
		return e == nil && inside(at)
	}
	if isCronExpr(t.Interval) {
		for at := start; at.Before(end); at = at.Add(time.Minute) {
			if cronDue(t.Interval, at) && inside(at) {
				return true
			}
		}
		return false
	}
	if at, ok := previousHeartbeatScheduleAt(t, end.Add(-time.Nanosecond)); ok {
		return inside(at)
	}
	d, e := parseInterval(t.Interval)
	if e != nil || d <= 0 {
		return false
	}
	base := time.UnixMilli(t.CreatedAt)
	if t.CreatedAt == 0 {
		return true
	}
	at := base.Add(d)
	if at.Before(start) {
		n := (start.Sub(at) + d - 1) / d
		at = at.Add(n * d)
	}
	return inside(at)
}
