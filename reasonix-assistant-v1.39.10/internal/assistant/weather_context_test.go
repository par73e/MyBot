package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWeatherMorningPromptReadsSharedRulesWithoutFetchingOrWriting(t *testing.T) {
	home := t.TempDir()
	s := WeatherService{Home: home, Fetch: func(context.Context, string) (json.RawMessage, error) {
		t.Fatal("reading briefing rules must not fetch weather")
		return nil, nil
	}}
	tool := WeatherTool{Service: s}
	raw, err := tool.Execute(t.Context(), json.RawMessage(`{"action":"morning_prompt"}`))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Prompt string `json:"prompt"`
		SHA256 string `json:"prompt_sha256"`
		Mode   string `json:"reply_mode"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.Prompt == "" || out.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(out.Prompt))) || out.Mode != "plain_text" {
		t.Fatal("rules missing, corrupted or using background reply mode")
	}
	if strings.Contains(out.Prompt, "notify=true") || !strings.Contains(out.Prompt, "view=briefing") {
		t.Fatal("manual content leaks the notification envelope or loses full-day query requirements")
	}
	files, err := os.ReadDir(home)
	if err != nil || len(files) != 0 {
		t.Fatal("reading rules created configuration, state or tasks", files, err)
	}
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"action":"morning_prompt","path":"/etc/passwd"}`)); err == nil {
		t.Fatal("rules action accepted an arbitrary file argument")
	}

	// Scheduled tasks and manual replies must use one content source, with only
	// the scheduled notification contract and host date appended separately.
	configured, options := weatherFixture(t)
	if err := configured.MorningTick(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	jobs, err := (JobStore{Repository: NewRepository(configured.Home)}).List(t.Context())
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	if !strings.HasPrefix(jobs[0].Prompt, out.Prompt+"\n") || !strings.Contains(jobs[0].Prompt[len(out.Prompt):], "notify=true") {
		t.Fatal("manual and scheduled briefing content diverged")
	}
}

func personalStateFixture(t *testing.T, s WeatherService, now time.Time) map[string]json.RawMessage {
	t.Helper()
	text, err := ContextFor(t.Context(), s.Home, now)
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(text, "\n{") + 1
	end := strings.LastIndex(text, "\n</personal_assistant_current_state>")
	var result map[string]json.RawMessage
	if start < 1 || end < start || json.Unmarshal([]byte(text[start:end]), &result) != nil {
		t.Fatal("invalid context snapshot")
	}
	return result
}

func TestFutureMemoriesVisibleBeforeEventAndExpireWithoutLosingCurrentFacts(t *testing.T) {
	s, _ := weatherFixture(t)
	now := s.now()
	mem := Memories{Home: s.Home, Now: func() time.Time { return now }}
	body := "一个月后去杭州旅行三天，出发地点上海，目的地浙江杭州，11月24日至26日。"
	saved, err := mem.Apply(t.Context(), MemoryRequest{Action: "create", Subject: "下个月行程", Body: body, Kind: "short", StartsAt: now.AddDate(0, 1, 0).Format(time.RFC3339), ExpiresAt: now.AddDate(0, 1, 3).Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = mem.Apply(t.Context(), MemoryRequest{Action: "create", Subject: "语言", Body: "使用中文", Kind: "long"}); err != nil {
		t.Fatal(err)
	}
	state := personalStateFixture(t, s, now)
	if !strings.Contains(string(state["known_future_memories"]), body) || strings.Contains(string(state["active_memories"]), body) {
		t.Fatal("future plan hidden or treated as happening now", state)
	}
	listed, err := mem.Apply(t.Context(), MemoryRequest{Action: "list"})
	if err != nil || len(listed) != 2 || saved[0].Active(now) || !saved[0].Visible(now) {
		t.Fatal("future memory visibility/applicability wrong", listed, err)
	}
	candidate := string(state["weather_defaults"])
	if !strings.Contains(candidate, `"place_id":"home"`) || strings.Contains(candidate, `"latitude"`) {
		t.Fatal("default candidate missing or exposes private coordinates", candidate)
	}
	atStart := personalStateFixture(t, s, now.AddDate(0, 1, 0))
	if !strings.Contains(string(atStart["active_memories"]), body) || strings.Contains(string(atStart["known_future_memories"]), body) {
		t.Fatal("event time not reflected")
	}
	after := personalStateFixture(t, s, now.AddDate(0, 1, 3))
	if strings.Contains(string(after["active_memories"]), body) || strings.Contains(string(after["known_future_memories"]), body) || !strings.Contains(string(after["active_memories"]), "使用中文") {
		t.Fatal("expired plan still applied or long memory lost")
	}
	if _, err = mem.Apply(t.Context(), MemoryRequest{Action: "archive", ID: saved[0].ID, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	archived := personalStateFixture(t, s, now)
	if strings.Contains(string(archived["known_future_memories"]), body) {
		t.Fatal("archived future memory resurrected")
	}
}

func TestWeatherDefaultReferenceUsesTargetDateWithoutFetchingWeather(t *testing.T) {
	s, _ := weatherFixture(t)
	var calls atomic.Int64
	s.Fetch = func(context.Context, string) (json.RawMessage, error) {
		calls.Add(1)
		return nil, fmt.Errorf("unexpected network")
	}
	out, err := s.Query(t.Context(), WeatherRequest{Action: "status", Date: "2026-10-26"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if calls.Load() != 0 || !strings.Contains(string(raw), `"place_id":"school"`) || !strings.Contains(string(raw), `"date":"2026-10-26"`) {
		t.Fatal("status fetched weather or selected wrong date", string(raw))
	}
	if _, err = s.Query(t.Context(), WeatherRequest{Action: "status", Date: "bad"}); err == nil {
		t.Fatal("invalid reference date accepted")
	}
}

func TestWeatherLocationIdentityAndRegionStayTogetherAcrossWeekdays(t *testing.T) {
	s, _ := weatherFixture(t)
	start, _ := time.Parse(time.RFC3339, "2026-10-19T01:00:00+08:00")
	for i := range 7 {
		now := start.AddDate(0, 0, i)
		t.Run(now.Weekday().String(), func(t *testing.T) {
			state := personalStateFixture(t, s, now)
			raw := state["weather_defaults"]
			var reference struct {
				Places    map[string]weatherPlaceReference `json:"places"`
				Candidate weatherCandidateReference        `json:"candidate"`
			}
			if err := json.Unmarshal(raw, &reference); err != nil {
				t.Fatal(err)
			}
			key, region := "school", "School district"
			if now.Weekday() == time.Saturday || now.Weekday() == time.Sunday {
				key, region = "home", "Home district"
			}
			c := reference.Candidate
			if c.PlaceID != key || c.Region != region || c.Name != "Fixture "+key || c.weatherPlaceReference != reference.Places[key] {
				t.Fatal("candidate identity or attributes crossed between places", string(raw))
			}
			for id, place := range reference.Places {
				if place.PlaceID != id || place.Region == "" {
					t.Fatal("place dictionary lost explicit identity or region", string(raw))
				}
			}
			if strings.Contains(string(raw), `"latitude"`) || strings.Contains(string(raw), `"longitude"`) {
				t.Fatal("ordinary context exposed coordinates")
			}
		})
	}
}

func TestWeatherMissingRegionStaysUnknownAndExplicitPlaceKeepsCoordinates(t *testing.T) {
	s, _ := weatherFixture(t)
	l, err := s.locations()
	if err != nil {
		t.Fatal(err)
	}
	p := l.Places["home"]
	p.Region = ""
	l.Places["home"] = p
	writeWeatherFixture(t, filepath.Join(s.Home, "locations.json"), l)
	out, err := s.Query(t.Context(), WeatherRequest{Action: "status"})
	if err != nil {
		t.Fatal(err)
	}
	c := out.(map[string]any)["candidate"].(weatherCandidateReference)
	if c.PlaceID != "home" || c.Region != "" {
		t.Fatal("legacy home inherited another place's region", c)
	}
	s.Fetch = func(_ context.Context, path string) (json.RawMessage, error) {
		if !strings.Contains(path, "/30.00/120.00") {
			t.Fatal("explicit school query used another place's coordinates", path)
		}
		return json.RawMessage(`{"temperature":{"value":20,"unit":"C"}}`), nil
	}
	out, err = s.Query(t.Context(), WeatherRequest{Action: "query", Place: "school", LocationSource: "explicit_question", LocationReason: "User asks about school", Components: []string{"current"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.(map[string]any)["place"].(WeatherPlace); got != l.Places["school"] {
		t.Fatal("weather result detached name, region or coordinates from selected place", got)
	}
}

func TestWeatherAgentChoosesTravelCityBeforeMorningFetch(t *testing.T) {
	s, _ := weatherFixture(t)
	var paths []string
	var mu sync.Mutex
	raw := json.RawMessage(`{"temperature":{"value":20,"unit":"C"},"days":[],"hours":[],"indexes":[],"alerts":[],"minutely":[],"daily":[],"weatherDaily":{},"location":[{"id":"fixture-city"}]}`)
	s.Fetch = func(_ context.Context, path string) (json.RawMessage, error) {
		mu.Lock()
		paths = append(paths, path)
		mu.Unlock()
		return raw, nil
	}
	tool := WeatherTool{Service: s}
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"action":"query","view":"briefing"}`)); err == nil {
		t.Fatal("morning implicitly queried default without Agent choice")
	}
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"action":"query","place":"school","location_source":"schedule","components":["current"]}`)); err == nil {
		t.Fatal("model could mislabel a weekday error as schedule fallback")
	}
	if len(paths) != 0 {
		t.Fatal("weather fetched before location choice")
	}
	args := json.RawMessage(`{"action":"query","view":"briefing","latitude":30.27,"longitude":120.15,"label":"杭州","location_source":"memory","location_reason":"当前旅行记忆：今天在杭州"}`)
	result, err := tool.Execute(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Place   WeatherPlace `json:"place"`
		Default bool         `json:"location_is_schedule_default"`
		Reason  string       `json:"location_reason"`
	}
	if err = json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	if out.Place.Name != "杭州" || out.Place.Latitude != 30.27 || out.Default || !strings.Contains(out.Reason, "旅行记忆") {
		t.Fatal("Agent destination overwritten", out)
	}
	count := 0
	for _, path := range paths {
		if strings.Contains(path, "/weather/v1/") || strings.Contains(path, "/airquality/") || strings.Contains(path, "/weatheralert/") {
			count++
			if !strings.Contains(path, "/30.27/120.15") {
				t.Fatal("API used configured home instead of chosen destination", path)
			}
		}
	}
	if count != 6 {
		t.Fatal("morning did not fetch complete selected-city components", count)
	}
	// A trip in memory must not override an explicit query about school.
	mem := Memories{Home: s.Home, Now: s.Clock}
	if _, err = mem.Apply(t.Context(), MemoryRequest{Action: "create", Subject: "旅行", Body: "今天在杭州", Kind: "short", ExpiresAt: s.now().Add(72 * time.Hour).Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	result, err = tool.Execute(t.Context(), json.RawMessage(`{"action":"query","place":"school","components":["current"],"location_source":"explicit_question","location_reason":"用户明确问学校"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(result), &out); err != nil || out.Place.Name != "Fixture school" {
		t.Fatal("tool overrode explicit Agent selection using memory", result, err)
	}
}
