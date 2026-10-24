package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/heartbeat"
)

func weatherFixture(t *testing.T) (WeatherService, Options) {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "assistant"), 0700); err != nil {
		t.Fatal(err)
	}
	writeWeatherFixture(t, filepath.Join(home, "assistant", "weather.json"), WeatherConfig{Enabled: true, CredentialFile: filepath.Join(home, "credential.json"), LocationsFile: filepath.Join(home, "locations.json"), MorningEnabled: true, WakeTimes: map[string]string{"monday": "10:00", "tuesday": "08:00", "wednesday": "10:00", "thursday": "10:00", "friday": "08:00", "saturday": "11:00", "sunday": "11:00"}})
	defaults := map[string]string{}
	for _, day := range weatherWeekdays {
		defaults[day] = "school"
	}
	defaults["saturday"], defaults["sunday"] = "home", "home"
	writeWeatherFixture(t, filepath.Join(home, "locations.json"), weatherLocations{Timezone: "Asia/Shanghai", Defaults: defaults, Places: map[string]WeatherPlace{"school": {Name: "Fixture school", Region: "School district", Latitude: 30, Longitude: 120}, "home": {Name: "Fixture home", Region: "Home district", Latitude: 31, Longitude: 121}}})
	now, _ := time.Parse(time.RFC3339, "2026-10-24T11:00:00+08:00")
	return WeatherService{Home: home, Clock: func() time.Time { return now }}, Options{Workspace: home, Model: "fixture/model", Target: &heartbeat.Target{ConnectionID: "weixin", Domain: "weixin", ChatID: "owner", ChatType: "dm"}}
}
func writeWeatherFixture(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestWeatherTargetCivilDateAndQueryBounds(t *testing.T) {
	s, _ := weatherFixture(t)
	l, err := s.locations()
	if err != nil {
		t.Fatal(err)
	}
	// Before 06:00 still uses the actual Saturday, not Friday's assistant business day.
	now, _ := time.Parse(time.RFC3339, "2026-10-24T01:00:00+08:00")
	s.Clock = func() time.Time { return now }
	p, err := s.plan(WeatherRequest{}, l)
	if err != nil || p.Place.Name != "Fixture home" {
		t.Fatal(p, err)
	}
	p, err = s.plan(WeatherRequest{Date: "2026-10-26"}, l)
	if err != nil || p.Place.Name != "Fixture school" {
		t.Fatal(p, err)
	}
	p, err = s.plan(WeatherRequest{Date: "2026-10-26", Place: "home"}, l)
	if err != nil || p.Default || p.Place.Name != "Fixture home" {
		t.Fatal(p, err)
	}
	invalid := []WeatherRequest{{Start: "2026-10-24T18:00:00+08:00"}, {Date: "2026-10-23"}, {Date: "2026-11-05"}, {View: "briefing"}, {View: "briefing", Components: []string{"daily"}}, {Components: []string{"solar"}}, {Components: []string{"current", "current"}}, {Latitude: new(float64)}, {Start: "2026-10-24T00:00:00+08:00", End: "2026-10-27T00:00:00+08:00"}}
	for _, r := range invalid {
		if _, err = s.plan(r, l); err == nil {
			t.Fatalf("accepted invalid request: %+v", r)
		}
	}
	tool := WeatherTool{Service: s}
	if _, err = tool.Execute(t.Context(), json.RawMessage(`{"action":"query","url":"https://evil.invalid"}`)); err == nil {
		t.Fatal("arbitrary URL accepted")
	}
}
func TestWeatherPartialResultsCacheStalenessAndHourSelection(t *testing.T) {
	s, _ := weatherFixture(t)
	now := s.now()
	s.Clock = func() time.Time { return now }
	var calls atomic.Int64
	s.Fetch = func(_ context.Context, path string) (json.RawMessage, error) {
		calls.Add(1)
		if strings.Contains(path, "weatheralert") {
			return nil, errors.New("simulated API failure")
		}
		if strings.Contains(path, "hourly") {
			return json.RawMessage(`{"hours":[{"forecastTime":"2026-10-24T17:00:00+08:00"},{"forecastTime":"2026-10-24T18:00+08:00","precipitation":{"probability":0.8}},{"forecastTime":"2026-10-24T23:00:00+08:00"}]}`), nil
		}
		return json.RawMessage(`{"temperature":{"value":20,"unit":"C"}}`), nil
	}
	r := WeatherRequest{Action: "query", Start: "2026-10-24T18:00:00+08:00", End: "2026-10-24T23:00:00+08:00", Components: []string{"current", "hourly", "alerts"}}
	result, err := s.Query(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	parts := result.(map[string]any)["components"].(map[string]weatherPart)
	if parts["alerts"].Status != "unavailable" || !strings.Contains(string(parts["hourly"].Data), `"selected_hour_count":1`) {
		t.Fatal(parts)
	}
	_, err = s.Query(t.Context(), r)
	if err != nil || calls.Load() != 4 {
		t.Fatal("cache or partial retry", calls.Load(), err)
	}
	now = now.Add(11 * time.Minute)
	s.Fetch = func(context.Context, string) (json.RawMessage, error) { return nil, errors.New("offline") }
	part := s.part(t.Context(), "/weather/v1/current/31.00/121.00?lang=zh&localTime=true", 10*time.Minute)
	if part.Status != "stale" || part.FetchedAt == "" {
		t.Fatal(part)
	}
	now = now.Add(2 * time.Hour)
	part = s.part(t.Context(), "/weather/v1/current/31.00/121.00?lang=zh&localTime=true", 10*time.Minute)
	if part.Status != "unavailable" || len(part.Data) != 0 {
		t.Fatal("expired data surfaced", part)
	}
}
func TestWeatherDailyComparisonsPreserveZeroMissingAndUnits(t *testing.T) {
	s, _ := weatherFixture(t)
	l, _ := s.locations()
	p, _ := s.plan(WeatherRequest{}, l)
	part := filterWeatherPart("daily", weatherPart{Status: "fresh", Data: json.RawMessage(`{"days":[{"forecastStartTime":"2026-10-23T16:00Z","temperatureMax":{"value":0,"unit":"C"},"temperatureMin":{"value":-3,"unit":"C"}},{"forecastStartTime":"2026-10-25T00:00+08:00","temperatureMax":{"value":-2,"unit":"C"}}]}`)}, p)
	c := weatherComparisons(map[string]weatherPart{"daily": part}).(map[string]any)
	if c["target_day_range"] != float64(3) || c["next_day_high_minus_target_high"] != float64(-2) {
		t.Fatal(c)
	}
	part.Data = json.RawMessage(`{"days":[{"temperatureMax":{"unit":"C"},"temperatureMin":{"value":2,"unit":"C"}}]}`)
	c = weatherComparisons(map[string]weatherPart{"daily": part}).(map[string]any)
	if _, exists := c["target_day_range"]; exists {
		t.Fatal("missing became zero", c)
	}
	missing := filterWeatherPart("air_current", weatherPart{Status: "fresh", Data: json.RawMessage(`{}`)}, p)
	if missing.Status != "unavailable" {
		t.Fatal(missing)
	}
	empty := filterWeatherPart("air_hourly", weatherPart{Status: "fresh", Data: json.RawMessage(`{"hours":[]}`)}, p)
	if empty.Status != "no_coverage" {
		t.Fatal(empty)
	}
}
func TestWeatherMorningWakeWindowsDedupAndRestart(t *testing.T) {
	s, o := weatherFixture(t)
	store := JobStore{Repository: NewRepository(s.Home)}
	for day, hour := range map[string]string{"2026-10-26": "10:00", "2026-10-27": "08:00", "2026-10-28": "10:00", "2026-10-29": "10:00", "2026-10-30": "08:00", "2026-10-31": "11:00", "2026-11-01": "11:00"} {
		wake, _ := time.Parse(time.RFC3339, day+"T"+hour+":00+08:00")
		now := wake.Add(-time.Second)
		s.Clock = func() time.Time { return now }
		before, _ := store.List(t.Context())
		if err := s.MorningTick(t.Context(), o); err != nil {
			t.Fatal(err)
		}
		after, _ := store.List(t.Context())
		if len(before) != len(after) {
			t.Fatal("ran before wake")
		}
		now = wake
		if err := s.MorningTick(t.Context(), o); err != nil {
			t.Fatal(err)
		}
		restarted := NewWeatherService(s.Home)
		restarted.Clock = s.Clock
		if err := restarted.MorningTick(t.Context(), o); err != nil {
			t.Fatal(err)
		}
		after, _ = store.List(t.Context())
		if len(after) != len(before)+1 {
			t.Fatal("restart duplicated morning", after)
		}
		var job heartbeat.Task
		for _, j := range after {
			if j.ID == "weather-morning-"+strings.ReplaceAll(day, "-", "") {
				job = j
			}
		}
		if job.Target.ChatID != "owner" || job.Capability != "weather" || job.ApprovalMode != "read-only" || !job.DecideNotification {
			t.Fatal(job)
		}
		allowed, err := s.MorningAllows(t.Context(), job, wake.Add(time.Hour))
		if err != nil || allowed {
			t.Fatal("expired delivery allowed", err)
		}
	}
	// A late restart must not create an old morning announcement.
	now, _ := time.Parse(time.RFC3339, "2026-11-02T11:00:00+08:00")
	s.Clock = func() time.Time { return now }
	if err := s.MorningTick(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	jobs, _ := store.List(t.Context())
	if len(jobs) != 7 {
		t.Fatal("late catch-up created task", len(jobs))
	}
}
func TestWeatherMorningRetryLimitsAndReadReceipts(t *testing.T) {
	s, o := weatherFixture(t)
	now := s.now()
	s.Clock = func() time.Time { return now }
	if err := s.MorningTick(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(s.Home)
	store := JobStore{Repository: repo}
	jobs, _ := store.List(t.Context())
	job := jobs[0]
	job.Enabled = false
	job.LastRunAt = now.UnixMilli()
	job.History = []heartbeat.Run{{StartedAt: now.UnixMilli(), Status: "failed"}}
	save := func() {
		t.Helper()
		if err := repo.transact(t.Context(), func(tx *sql.Tx) error { return saveEntity(t.Context(), tx, "job", job.ID, job) }); err != nil {
			t.Fatal(err)
		}
	}
	save()
	if err := s.MorningTick(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	jobs, _ = store.List(t.Context())
	if jobs[0].Enabled {
		t.Fatal("retry without backoff")
	}
	now = now.Add(time.Minute)
	if err := s.MorningTick(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	jobs, _ = store.List(t.Context())
	if !jobs[0].Enabled || jobs[0].LastRunAt != 0 {
		t.Fatal("failed morning not retried")
	}
	job.History = append(job.History, heartbeat.Run{StartedAt: now.UnixMilli(), Status: "failed"}, heartbeat.Run{StartedAt: now.UnixMilli(), Status: "failed"})
	save()
	now = now.Add(4 * time.Minute)
	if err := s.MorningTick(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	jobs, _ = store.List(t.Context())
	if jobs[0].Enabled {
		t.Fatal("unbounded morning retries")
	}
	t.Setenv("REASONIX_ASSISTANT_WEATHER_BACKGROUND", "1")
	t.Setenv("REASONIX_ASSISTANT_JOB_ID", job.ID)
	t.Setenv("REASONIX_ASSISTANT_WEATHER_ATTEMPT", "1234")
	s.Fetch = func(context.Context, string) (json.RawMessage, error) { return nil, errors.New("offline") }
	if err := s.VerifyWeatherRead(t.Context(), job.ID, 1234); err == nil {
		t.Fatal("unread briefing accepted")
	}
	if _, err := s.Query(t.Context(), WeatherRequest{Action: "query", View: "briefing", Place: "default"}); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyWeatherRead(t.Context(), job.ID, 1234); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyWeatherRead(t.Context(), job.ID, 1235); err == nil {
		t.Fatal("old attempt receipt reused")
	}
	if _, err := s.Query(t.Context(), WeatherRequest{Action: "query", Components: []string{"current"}}); err == nil {
		t.Fatal("nonbriefing background query accepted")
	}
}

func TestWeatherToolOutputStaysValidBelowAgentBudget(t *testing.T) {
	parts := map[string]weatherPart{"alerts": {Status: "fresh", Data: json.RawMessage(`{"alerts":[{"headline":"` + strings.Repeat("x", 40000) + `"}]}`)}, "current": {Status: "fresh", Data: json.RawMessage(`{"temperature":{"value":20}}`)}}
	out := map[string]any{"components": parts}
	boundWeatherOutput(out, parts)
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > 30<<10 || parts["alerts"].Status != "output_limited" || parts["current"].Status != "fresh" {
		t.Fatal("output clipped or lost valid partial data", len(raw), err)
	}
}

func TestWeatherAlertDatesAreExplicitAndExpiryIsChecked(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-10-24T11:00:00+08:00")
	part := weatherAlertTimes(weatherPart{Status: "fresh", Data: json.RawMessage(`{"alerts":[{"effectiveTime":"2026-10-23T22:00+08:00","expireTime":"2026-10-24T22:00+08:00"},{"expireTime":"2026-10-23T22:00+08:00"}]}`)}, now)
	var v struct {
		Alerts []map[string]any `json:"alerts"`
	}
	if err := json.Unmarshal(part.Data, &v); err != nil {
		t.Fatal(err)
	}
	if v.Alerts[0]["effectiveTime_beijing"] != "2026-10-23 22:00" || v.Alerts[0]["expired_at_query"] != false || v.Alerts[1]["expired_at_query"] != true {
		t.Fatal(v)
	}
}
