package assistant

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/heartbeat"
)

func TestAlertDistrictScope(t *testing.T) {
	for _, tc := range []struct {
		area, district string
		want           bool
	}{
		{"全市", "", true},
		{"全市", "宝山区", true},
		{"徐汇区", "", false},
		{"宝山区", "徐汇区", false},
		{"宝山区", "宝山区", true},
		{"中心城区", "徐汇区", true},
		{"中心城区", "宝山区", false},
		{"中心城区", "", false},
		{"未知", "宝山区", false},
	} {
		if got := alertAppliesToDistrict(tc.area, tc.district); got != tc.want {
			t.Errorf("area %q district %q: got %v want %v", tc.area, tc.district, got, tc.want)
		}
	}
}

func TestAlertScheduleFilteringAndAdmission(t *testing.T) {
	hs, s, f := newAlertFixture(t)
	path := filepath.Join(s.Home, "assistant", "alert-scope.json")
	if err := os.WriteFile(path, []byte(`{"weekday":"宝山区","weekend":"徐汇区"}`), 0600); err != nil {
		t.Fatal(err)
	}
	// 27 October is Tuesday; the weekday fixture uses a non-central district.
	weekdayAlert := fixtureOfficialAlert(t, f.now.Add(-time.Minute), "黄色")
	weekdayAlert.Area, weekdayAlert.Key = "宝山区", mailHash("宝山区:大风")
	weekendAlert := weekdayAlert
	weekendAlert.Area, weekendAlert.Key = "徐汇区", mailHash("徐汇区:大风")
	for _, a := range []OfficialAlert{weekendAlert, weekdayAlert} {
		if err := s.ingest(t.Context(), a, hs.Options, 1); err != nil {
			t.Fatal(err)
		}
	}
	var queued AlertEvent
	for _, event := range alertEventsTest(t, s) {
		if event.Alert.Area == "徐汇区" && event.JobID != "" {
			t.Fatal("weekday queued a weekend district warning")
		}
		if event.Alert.Area == "宝山区" {
			queued = event
		}
	}
	if queued.JobID == "" {
		t.Fatal("weekday did not queue weekday district warning")
	}
	task := heartbeat.Task{ID: queued.JobID, AlertEventID: queued.ID}
	if ok, err := s.Admission(t.Context(), task); err != nil || !ok {
		t.Fatal("relevant warning rejected", err)
	}
	// A scope change invalidates a pending warning while the observation is fresh.
	if err := os.WriteFile(path, []byte(`{"weekday":"徐汇区","weekend":"徐汇区"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Admission(t.Context(), task); ok || !errors.Is(err, heartbeat.ErrOccurrenceChanged) {
		t.Fatal("out-of-scope pending warning allowed", err)
	}
	if err := os.WriteFile(path, []byte(`{"weekday":"宝山区","weekend":"徐汇区"}`), 0600); err != nil {
		t.Fatal(err)
	}
	f.now = timeMust("2026-10-31T10:00:00+08:00")
	if !s.alertInScope(weekendAlert) || s.alertInScope(weekdayAlert) || !s.alertInScope(OfficialAlert{Area: "全市"}) {
		t.Fatal("weekend scope mismatch")
	}
	if err := os.WriteFile(path, []byte(`invalid`), 0600); err != nil {
		t.Fatal(err)
	}
	if s.alertInScope(weekendAlert) || !s.alertInScope(OfficialAlert{Area: "全市"}) {
		t.Fatal("invalid configuration did not fail closed")
	}
}
