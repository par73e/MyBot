package assistant

import (
	"context"
	"errors"
	"slices"
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
	applyLocationWeatherReference(reference, l, home, "", now)
	return reference
}
func (s WeatherService) status(r WeatherRequest, l weatherLocations) (any, error) {
	out, err := weatherDefaultReference(l, r.Date, s.now())
	if err != nil {
		return nil, err
	}
	applyLocationWeatherReference(out, l, s.Home, r.Date, s.now())
	cfg, _ := s.Config()
	out["enabled"], out["morning_enabled"], out["wake_times"] = true, cfg.MorningEnabled, cfg.WakeTimes
	if c, e := (HeartbeatService{Home: s.Home}).Config(context.Background()); e == nil {
		times := map[string]string{}
		for day, w := range c.Weekly {
			times[day] = w.Start
		}
		date := r.Date
		if date == "" {
			date = s.now().In(mustShanghai()).Format("2006-01-02")
		}
		tr := c.Triggers["morning"]
		_, _, valid := c.TriggerBounds(tr, date)
		out["morning_enabled"], out["wake_times"], out["schedule_source"] = c.ModuleEnabled["weather_briefing"] && tr.Enabled && valid && slices.Contains(c.Selected(tr, date), "weather_briefing"), times, "assistant_heartbeat"
		out["detailed_weather_module_enabled"] = c.ModuleEnabled["weather_briefing"]
		out["wake_modules"] = c.Selected(tr, date)
	}
	return out, nil
}

// A date-only future query uses noon as a reference; exact plans and weekly
// boundaries remain visible so the Agent can choose the actual requested time.
func applyLocationWeatherReference(out map[string]any, l weatherLocations, home, date string, now time.Time) {
	at := now
	if date != "" && date != now.In(mustShanghai()).Format("2006-01-02") {
		if d, e := time.ParseInLocation("2006-01-02", date, mustShanghai()); e == nil {
			at = d.Add(12 * time.Hour)
		}
	}
	service := LocationService{Home: home}
	estimate := service.Resolve(at)
	if !estimate.Configured {
		return
	}
	out["location_estimate"] = estimate
	if c, e := service.config(); e == nil {
		out["location_weekly_boundaries"] = c.Weekly
		out["location_plans"] = c.Plans
	}
	if !estimate.Known {
		out["candidate"] = weatherCandidateReference{weatherPlaceReference: weatherPlaceReference{Name: "地点待确认"}, Date: at.In(mustShanghai()).Format("2006-01-02"), CalendarBasis: "Location plan unavailable or conflicting; ask the owner before choosing a destination"}
		out["usage"] = out["usage"].(string) + " Location estimate is unknown or conflicting. Do not silently use the legacy weekday destination; clarify the destination first."
		return
	}
	p, ok := l.Places[estimate.PlaceID]
	ref := weatherPlaceReference{Name: estimate.Place.Name, Region: estimate.Place.District}
	if ok {
		ref = weatherPlaceReference{PlaceID: estimate.PlaceID, Name: p.Name, Region: p.Region}
	}
	out["candidate"] = weatherCandidateReference{weatherPlaceReference: ref, Date: at.In(mustShanghai()).Format("2006-01-02"), Weekday: weatherWeekdays[at.In(mustShanghai()).Weekday()], CalendarBasis: "Based on confirmed plans and weekly time boundaries at " + at.In(mustShanghai()).Format(time.RFC3339) + "; date-only future reference uses noon; never GPS"}
	out["usage"] = out["usage"].(string) + " Confirmed location_plans override the weekly baseline only within their starts_at/ends_at. The legacy default_by_weekday table is superseded by location_estimate and the time-boundary routine. Empty candidate.place_id means this itinerary destination has no configured weather coordinates: lookup the actual destination before querying; never silently substitute school/home. For the current time use location_estimate; for another time use the exposed boundaries and plans."
}
