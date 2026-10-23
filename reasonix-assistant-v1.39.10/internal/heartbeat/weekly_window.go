package heartbeat

import (
	"errors"
	"time"
)

// DailyWindow belongs to its start weekday; overnight hours spill into the next day.
type DailyWindow struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start,omitempty"`
	End     string `json:"end,omitempty"`
}

type WeeklySchedule map[string]DailyWindow

var weekdayKeys = [...]string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

func (w WeeklySchedule) Validate() error {
	if len(w) != 7 {
		return errors.New("weeklySchedule must specify all seven weekdays")
	}
	for _, day := range weekdayKeys {
		v, ok := w[day]
		if !ok {
			return errors.New("weeklySchedule uses lowercase English weekday names")
		}
		if !v.Enabled && v.Start == "" && v.End == "" {
			continue
		}
		if _, _, ok := parseHeartbeatClock(v.Start); !ok {
			return errors.New("weekly start must use HH:MM")
		}
		if _, _, ok := weeklyEndClock(v.End); !ok {
			return errors.New("weekly end must use HH:MM")
		}
		if v.Start == v.End {
			return errors.New("weekly endpoints must differ; use 00:00 to 24:00 for a full day")
		}
	}
	return nil
}

// Allows evaluates local civil weekdays, independent of assistant conversation days.
func (w WeeklySchedule) Allows(now time.Time) bool {
	minute := now.Hour()*60 + now.Minute()
	today := int(now.Weekday())
	current := w[weekdayKeys[today]]
	previous := w[weekdayKeys[(today+6)%7]]
	lo, hi, ok := windowMinutes(current)
	if ok && ((lo < hi && minute >= lo && minute < hi) || (lo > hi && minute >= lo)) {
		return true
	}
	lo, hi, ok = windowMinutes(previous)
	return ok && lo > hi && minute < hi
}

func windowMinutes(v DailyWindow) (int, int, bool) {
	sh, sm, sok := parseHeartbeatClock(v.Start)
	eh, em, eok := weeklyEndClock(v.End)
	return sh*60 + sm, eh*60 + em, v.Enabled && sok && eok
}

func weeklyEndClock(s string) (int, int, bool) {
	if s == "24:00" {
		return 24, 0, true
	}
	return parseHeartbeatClock(s)
}
