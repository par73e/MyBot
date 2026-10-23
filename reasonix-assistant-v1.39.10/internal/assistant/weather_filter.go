package assistant

import (
	"encoding/json"
	"math"
	"time"
)

func filterWeatherPart(kind string, part weatherPart, p weatherPlan) weatherPart {
	if len(part.Data) == 0 {
		return part
	}
	var object map[string]any
	if json.Unmarshal(part.Data, &object) != nil || object == nil {
		return weatherPart{Status: "unavailable", Error: "invalid weather object"}
	}
	required := map[string]string{"current": "temperature", "daily": "days", "hourly": "hours", "air_current": "indexes", "air_hourly": "hours", "air_daily": "days", "alerts": "alerts", "minutely": "minutely", "indices": "daily", "history": "weatherDaily", "astronomy": "sunrise"}
	field := required[kind]
	if _, ok := object[field]; field != "" && !ok {
		return weatherPart{Status: "unavailable", FetchedAt: part.FetchedAt, Error: "provider omitted required weather data"}
	}
	if kind == "hourly" || kind == "air_hourly" {
		selectWeatherHours(kind, object, p, &part)
	}
	if kind == "daily" || kind == "air_daily" {
		source, _ := object["days"].([]any)
		selected := []any{}
		for _, v := range source {
			row, ok := v.(map[string]any)
			if !ok {
				continue
			}
			value, _ := row["forecastStartTime"].(string)
			at, err := weatherProviderTime(value)
			if err == nil && !at.Before(p.Date) && at.Before(p.End.AddDate(0, 0, 2)) {
				selected = append(selected, row)
			}
		}
		object["days"] = selected
		object["context_note"] = "Includes up to two following days for trend comparison; answer requested date/range. Night period may extend into following morning."
		if len(selected) == 0 && part.Status != "stale" {
			part.Status = "no_coverage"
		}
	}
	if kind == "indices" {
		source, _ := object["daily"].([]any)
		selected := []any{}
		for _, v := range source {
			row, ok := v.(map[string]any)
			if ok && row["date"] == p.Date.Format("2006-01-02") {
				selected = append(selected, row)
			}
		}
		object["daily"] = selected
		if len(selected) == 0 && part.Status != "stale" {
			part.Status = "no_coverage"
		}
	}
	compactWeatherObject(kind, object)
	part.Data, _ = json.Marshal(object)
	if len(part.Data) > 192<<10 {
		return weatherPart{Status: "unavailable", Error: "weather component exceeds output limit"}
	}
	return part
}
func weatherComparisons(parts map[string]weatherPart) any {
	p, ok := parts["daily"]
	if !ok || p.Status == "stale" || p.Status == "unavailable" {
		return nil
	}
	var data struct {
		Days []struct {
			Start string `json:"forecastStartTime"`
			Max   struct {
				Value *float64 `json:"value"`
				Unit  string   `json:"unit"`
			} `json:"temperatureMax"`
			Min struct {
				Value *float64 `json:"value"`
				Unit  string   `json:"unit"`
			} `json:"temperatureMin"`
		} `json:"days"`
	}
	if json.Unmarshal(p.Data, &data) != nil || len(data.Days) == 0 {
		return nil
	}
	first := data.Days[0]
	out := map[string]any{"basis": "same requested coordinates, forecast-to-forecast; not observed historical temperatures"}
	if first.Max.Value != nil && first.Min.Value != nil && first.Max.Unit == first.Min.Unit {
		out["target_day_range"] = math.Round((*first.Max.Value-*first.Min.Value)*10) / 10
		out["unit"] = first.Max.Unit
	}
	if len(data.Days) > 1 {
		next := data.Days[1]
		if first.Max.Value != nil && next.Max.Value != nil && first.Max.Unit == next.Max.Unit {
			out["next_day_high_minus_target_high"] = math.Round((*next.Max.Value-*first.Max.Value)*10) / 10
			out["target_period_start"] = first.Start
			out["next_period_start"] = next.Start
			out["unit"] = first.Max.Unit
		}
	}
	return out
}

func selectWeatherHours(kind string, object map[string]any, p weatherPlan, part *weatherPart) {
	source, _ := object["hours"].([]any)
	selected := []any{}
	for _, v := range source {
		row, ok := v.(map[string]any)
		if !ok {
			continue
		}
		value, _ := row["forecastTime"].(string)
		at, err := weatherProviderTime(value)
		if err != nil || at.Before(p.Start) || !at.Before(p.End) {
			continue
		}
		if kind == "air_hourly" {
			delete(row, "pollutants")
		}
		selected = append(selected, row)
	}
	object["hours"] = selected
	object["selected_hour_count"] = len(selected)
	if len(selected) == 0 && part.Status != "stale" {
		part.Status = "no_coverage"
	}
}

// QWeather uses ISO8601 with minute precision as well as full RFC3339.
func weatherProviderTime(value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, value)
	if err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02T15:04Z07:00", value)
}
