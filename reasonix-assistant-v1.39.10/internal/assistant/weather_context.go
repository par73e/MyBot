package assistant

import (
	"errors"
	"time"
)

// These are candidate locations, never a decision based on personal memories.
// The Agent receives the reference and memories before choosing query arguments.
func weatherDefaultReference(l weatherLocations, date string, now time.Time) (map[string]any, error) {
	zone, _ := time.LoadLocation(l.Timezone)
	if date == "" {
		date = now.In(zone).Format("2006-01-02")
	}
	day, err := time.ParseInLocation("2006-01-02", date, zone)
	if err != nil {
		return nil, errors.New("date must be YYYY-MM-DD")
	}
	key := l.Defaults[weatherWeekdays[day.Weekday()]]
	names := map[string]string{}
	for id, p := range l.Places {
		names[id] = p.Name
	}
	return map[string]any{"timezone": l.Timezone, "default_by_weekday": l.Defaults, "places": names, "candidate": map[string]any{"date": date, "place_id": key, "name": l.Places[key].Name, "weekday": weatherWeekdays[day.Weekday()], "calendar_basis": "Civil date 00:00-24:00, independent of conversation rotation at 06:00"}, "usage": "Schedule reference only, not GPS. Agent must consider the question and current/future memories before choosing the actual query location. For schedule fallback use candidate.place_id exactly; do not recompute weekday from a 06:00 conversation date. For another date call status with that date."}, nil
}
func weatherContext(home string, now time.Time) any {
	l, err := NewWeatherService(home).locations()
	if err != nil {
		return nil
	}
	reference, err := weatherDefaultReference(l, "", now)
	if err != nil {
		return nil
	}
	return reference
}
func (s WeatherService) status(r WeatherRequest, l weatherLocations) (any, error) {
	out, err := weatherDefaultReference(l, r.Date, s.now())
	if err != nil {
		return nil, err
	}
	cfg, _ := s.Config()
	out["enabled"], out["morning_enabled"], out["wake_times"] = true, cfg.MorningEnabled, cfg.WakeTimes
	return out, nil
}
