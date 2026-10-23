package assistant

import (
	"encoding/json"
	"sort"
	"time"
)

// Keep useful hourly weather fields while avoiding repeated AQI advice/color
// objects. Current AQI retains pollutant detail and the official health advice.
func compactWeatherObject(kind string, object map[string]any) {
	if kind == "history" {
		delete(object, "weatherHourly")
	}
	if kind != "hourly" && kind != "air_hourly" {
		return
	}
	rows, _ := object["hours"].([]any)
	for _, v := range rows {
		row, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if kind == "hourly" {
			for _, key := range []string{"pressure", "dewPoint", "cloudCover"} {
				delete(row, key)
			}
			continue
		}
		indexes, _ := row["indexes"].([]any)
		for _, v := range indexes {
			index, ok := v.(map[string]any)
			if !ok {
				continue
			}
			for _, key := range []string{"color", "health", "name", "aqiDisplay"} {
				delete(index, key)
			}
		}
	}
}

// The Agent transport clips tool text at 32 KiB. Return valid JSON below that
// budget, with explicit per-component limitations rather than silently clipping.
func boundWeatherOutput(out map[string]any, parts map[string]weatherPart) {
	type size struct {
		kind string
		n    int
	}
	sizes := make([]size, 0, len(parts))
	for kind, part := range parts {
		sizes = append(sizes, size{kind, len(part.Data)})
	}
	sort.Slice(sizes, func(i, j int) bool { return sizes[i].n > sizes[j].n })
	for _, item := range sizes {
		raw, _ := json.Marshal(out)
		if len(raw) <= 30<<10 {
			return
		}
		part := parts[item.kind]
		parts[item.kind] = weatherPart{Status: "output_limited", FetchedAt: part.FetchedAt, Error: "Data exceeds combined tool output budget; unavailable in this response. Request fewer components or a narrower time range. Do not assume normal conditions."}
	}
}

func weatherAlertTimes(part weatherPart, now time.Time) weatherPart {
	if len(part.Data) == 0 {
		return part
	}
	var object map[string]any
	if json.Unmarshal(part.Data, &object) != nil {
		return part
	}
	alerts, _ := object["alerts"].([]any)
	zone, _ := time.LoadLocation("Asia/Shanghai")
	for _, v := range alerts {
		row, ok := v.(map[string]any)
		if !ok {
			continue
		}
		for _, field := range []string{"issuedTime", "effectiveTime", "onsetTime", "expireTime"} {
			value, _ := row[field].(string)
			at, err := weatherProviderTime(value)
			if err != nil {
				continue
			}
			row[field+"_beijing"] = at.In(zone).Format("2006-01-02 15:04")
			if field == "expireTime" {
				row["expired_at_query"] = !now.Before(at)
			}
		}
	}
	part.Data, _ = json.Marshal(object)
	return part
}
