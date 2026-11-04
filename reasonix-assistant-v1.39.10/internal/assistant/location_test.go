package assistant

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reasonix/internal/tool"
	"testing"
	"time"
)

func locationFixture(t *testing.T) LocationService {
	t.Helper()
	s := LocationService{Home: t.TempDir()}
	c := LocationRoutine{Revision: 1, Places: map[string]LocationPlace{"school": {"临港学校", "上海", "浦东新区"}, "home": {"家", "上海", "黄浦区"}, "trip": {"杭州出行", "杭州", ""}}, Weekly: []LocationTransition{{0, "17:00", "school"}, {5, "19:00", "home"}}, Plans: []LocationPlan{}}
	if e := os.MkdirAll(filepath.Dir(s.path()), 0700); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(c)
	if e := os.WriteFile(s.path(), b, 0600); e != nil {
		t.Fatal(e)
	}
	return s
}
func TestLocationWeeklyMinuteBoundaries(t *testing.T) {
	s := locationFixture(t)
	for _, c := range []struct{ at, id string }{{"2026-11-06T18:59:59+08:00", "school"}, {"2026-11-06T19:00:00+08:00", "home"}, {"2026-11-07T12:00:00+08:00", "home"}, {"2026-11-08T16:59:59+08:00", "home"}, {"2026-11-08T17:00:00+08:00", "school"}, {"2026-11-09T00:00:00+08:00", "school"}, {"2026-11-08T09:00:00Z", "school"}} {
		if got := s.Resolve(timeMust(c.at)); !got.Known || got.PlaceID != c.id {
			t.Errorf("%s: %+v", c.at, got)
		}
	}
}
func TestLocationPlansOverrideOnlyTheirExactIntervalAndConflictSafely(t *testing.T) {
	s := locationFixture(t)
	ctx := t.Context()
	_, e := s.Apply(ctx, LocationRequest{Action: "set_plan", Revision: 1, ID: "trip1", PlaceID: "trip", StartsAt: "2026-11-06T12:00:00+08:00", EndsAt: "2026-11-07T12:00:00+08:00", Label: "杭州出行"})
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range []struct{ at, id string }{{"2026-11-06T11:59:59+08:00", "school"}, {"2026-11-06T12:00:00+08:00", "trip"}, {"2026-11-07T12:00:00+08:00", "home"}} {
		if s.Resolve(timeMust(c.at)).PlaceID != c.id {
			t.Fatal(c)
		}
	}
	_, e = s.Apply(ctx, LocationRequest{Action: "set_plan", Revision: 2, ID: "conflict", PlaceID: "school", StartsAt: "2026-11-06T12:00:00+08:00", EndsAt: "2026-11-06T13:00:00+08:00"})
	if e != nil {
		t.Fatal(e)
	}
	if got := s.Resolve(timeMust("2026-11-06T12:30:00+08:00")); got.Known || got.Source != "conflicting_plans" {
		t.Fatal(got)
	}
	if _, e = s.Apply(ctx, LocationRequest{Action: "remove_plan", Revision: 1, ID: "trip1"}); e == nil {
		t.Fatal("stale revision changed itinerary")
	}
}
func TestLocationSharedWithWeatherAndAlertAdmission(t *testing.T) {
	s := locationFixture(t)
	now := timeMust("2026-11-06T19:00:00+08:00")
	a := AlertService{Home: s.Home, Clock: func() time.Time { return now }}
	if a.alertInScope(OfficialAlert{Area: "浦东新区"}) || !a.alertInScope(OfficialAlert{Area: "黄浦区"}) || !a.alertInScope(OfficialAlert{Area: "全市"}) {
		t.Fatal("weekly scope mismatch")
	}
	l := weatherLocations{Timezone: "Asia/Shanghai", Defaults: map[string]string{"friday": "school"}, Places: map[string]WeatherPlace{"school": {Name: "临港学校"}, "home": {Name: "家"}}}
	out, _ := weatherDefaultReference(l, "", now)
	applyLocationWeatherReference(out, l, s.Home, "", now)
	if out["candidate"].(weatherCandidateReference).PlaceID != "home" {
		t.Fatal(out)
	}
	_, e := s.Apply(t.Context(), LocationRequest{Action: "set_plan", Revision: 1, ID: "away", PlaceID: "trip", StartsAt: now.Format(time.RFC3339), EndsAt: now.Add(time.Hour).Format(time.RFC3339)})
	if e != nil {
		t.Fatal(e)
	}
	if a.alertInScope(OfficialAlert{Area: "全市"}) {
		t.Fatal("Shanghai alert applies to an outside-Shanghai itinerary")
	}
	applyLocationWeatherReference(out, l, s.Home, "", now)
	if out["candidate"].(weatherCandidateReference).PlaceID != "" {
		t.Fatal("invented weather coordinates for a new destination")
	}
}
func TestLocationInvalidConfigFailsClosedAndWritesNeedForegroundSource(t *testing.T) {
	s := locationFixture(t)
	if e := os.WriteFile(s.path(), []byte("invalid"), 0600); e != nil {
		t.Fatal(e)
	}
	a := AlertService{Home: s.Home}
	if a.alertInScope(OfficialAlert{Area: "浦东新区"}) || !a.alertInScope(OfficialAlert{Area: "全市"}) {
		t.Fatal("invalid settings widened scope")
	}
	s = locationFixture(t)
	toolUnderTest := LocationTool{Service: s}
	args := json.RawMessage(`{"action":"set_plan","revision":1,"place_id":"home","starts_at":"2099-11-06T12:00:00+08:00","ends_at":"2099-11-07T12:00:00+08:00","source_quote":"明天回家"}`)
	if _, e := toolUnderTest.Execute(context.Background(), args); e == nil {
		t.Fatal("untrusted write allowed")
	}
	ctx := tool.WithForegroundAuthorization(t.Context(), tool.ForegroundAuthorization{Text: "明天回家", MessageID: "owner-message"})
	if _, e := toolUnderTest.Execute(ctx, args); e != nil {
		t.Fatal(e)
	}
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "1")
	if _, e := toolUnderTest.Execute(ctx, args); e == nil {
		t.Fatal("background write allowed")
	}
}
func TestDeliveredAlertHistoryPaginatesWithoutDroppingOlderNotifications(t *testing.T) {
	events := []AlertEvent{}
	for i := 0; i < 53; i++ {
		events = append(events, AlertEvent{ID: string(rune('A' + i)), DeliveredAt: time.Unix(int64(i+1), 0)})
	}
	events = append(events, AlertEvent{ID: "pending"})
	seen := map[string]bool{}
	for offset := 0; offset < 53; offset += 20 {
		page := deliveredAlertHistory(events, offset, 20)
		rows := page["events"].([]AlertEvent)
		if page["total"].(int) != 53 {
			t.Fatal(page)
		}
		for _, e := range rows {
			if seen[e.ID] {
				t.Fatal("duplicate history")
			}
			seen[e.ID] = true
		}
	}
	if len(seen) != 53 || seen["pending"] {
		t.Fatal("history omitted or invented a delivered event")
	}
}

func TestConflictingLocationDoesNotBecomeALegacyWeatherDefault(t *testing.T) {
	s := locationFixture(t)
	at := timeMust("2026-11-06T12:00:00+08:00")
	for i, id := range []string{"home", "school"} {
		_, e := s.Apply(t.Context(), LocationRequest{Action: "set_plan", Revision: i + 1, ID: id, PlaceID: id, StartsAt: at.Format(time.RFC3339), EndsAt: at.Add(time.Hour).Format(time.RFC3339)})
		if e != nil {
			t.Fatal(e)
		}
	}
	l := weatherLocations{Timezone: "Asia/Shanghai", Defaults: map[string]string{"friday": "school"}, Places: map[string]WeatherPlace{"school": {Name: "学校"}}}
	out, _ := weatherDefaultReference(l, "", at)
	applyLocationWeatherReference(out, l, s.Home, "", at)
	if out["candidate"].(weatherCandidateReference).PlaceID != "" {
		t.Fatal("conflicting plans silently became school weather")
	}
}
