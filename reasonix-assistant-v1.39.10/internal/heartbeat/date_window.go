package heartbeat

import (
	"errors"
	"fmt"
	"time"
)

// DateOverrides replaces the allowed hours for exactly one local civil date.
// Unlike weekly overnight windows, an override never extends past midnight.
// Other dates resume the normal daily/weekly rule without a reset job.
type DateOverrides map[string]DailyWindow

func (d DateOverrides) Validate() error {
	if len(d) > 366 {
		return errors.New("dateOverrides supports at most 366 dates")
	}
	for date, window := range d {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return fmt.Errorf("date override requires YYYY-MM-DD: %w", err)
		}
		if !window.Enabled {
			if window.Start != "" || window.End != "" {
				return errors.New("disabled date override must omit start and end")
			}
			continue
		}
		lo, hi, ok := windowMinutes(window)
		if !ok || lo >= hi {
			return errors.New("date override requires same-day HH:MM start < end; end may be 24:00")
		}
	}
	return nil
}

func (d DateOverrides) allows(now time.Time) (allowed, matched bool) {
	window, matched := d[now.Format("2006-01-02")]
	if !matched {
		return false, false
	}
	lo, hi, ok := windowMinutes(window)
	minute := now.Hour()*60 + now.Minute()
	return ok && lo < hi && minute >= lo && minute < hi, true
}
