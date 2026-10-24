package assistant

import (
	"context"
	"errors"
	"time"
)

// Location identity and region travel together. Coordinates stay in the weather
// service rather than being exposed in every ordinary conversation.
type weatherPlaceReference struct {
	PlaceID string `json:"place_id"`
	Name    string `json:"name"`
	Region  string `json:"region,omitempty"`
}

type weatherCandidateReference struct {
	weatherPlaceReference
	Date          string `json:"date"`
	Weekday       string `json:"weekday"`
	CalendarBasis string `json:"calendar_basis"`
}

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
	places := map[string]weatherPlaceReference{}
	for id, p := range l.Places {
		places[id] = weatherPlaceReference{PlaceID: id, Name: p.Name, Region: p.Region}
	}
	return map[string]any{
		"timezone": l.Timezone, "default_by_weekday": l.Defaults, "places": places,
		"candidate": weatherCandidateReference{
			weatherPlaceReference: places[key], Date: date, Weekday: weatherWeekdays[day.Weekday()],
			CalendarBasis: "Civil date 00:00-24:00, independent of conversation rotation at 06:00",
		},
		"usage": "Each places entry is one independent location, keyed by place_id. Select the relevant place_id first, then use name and region only from that SAME record. candidate is copied from places[candidate.place_id]; never combine one place's name with another place's region. A missing region is unknown, not inherited from another place. These records describe configured places; the candidate is a schedule reference, not GPS or confirmation of the owner's current physical location. Do not infer the owner's actual location or a security mismatch from this schedule. Agent must consider the question and current/future memories before choosing the actual query location. For schedule fallback use candidate.place_id exactly; do not recompute weekday from a 06:00 conversation date. For another date call status with that date.",
	}, nil
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
	if c, e := (HeartbeatService{Home: s.Home}).Config(context.Background()); e == nil {
		times := map[string]string{}
		for day, w := range c.Weekly {
			times[day] = w.Start
		}
		out["morning_enabled"], out["wake_times"], out["schedule_source"] = c.ModuleEnabled["weather_briefing"], times, "assistant_heartbeat"
	}
	return out, nil
}
