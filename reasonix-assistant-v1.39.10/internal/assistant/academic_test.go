package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reasonix/internal/heartbeat"
	"testing"
	"time"
)

func academicFixture(t *testing.T, home string) *AcademicCalendar {
	t.Helper()
	c := &AcademicCalendar{Version: 1, Revision: "test", School: "上海电力大学", ValidFrom: "2026-09-01", ValidTo: "2027-08-31", Terms: []AcademicTerm{{"上学期", "2026-09-14", "2027-01-22"}, {"下学期", "2027-03-01", "2027-07-09"}}, Closures: []AcademicClosure{{"2026-10-01", "2026-10-07", "国庆放假", "school"}, {"2027-01-01", "2027-01-01", "元旦", "school"}}, Adjustments: map[string]AcademicAdjustment{"2026-09-20": {true, "2026-10-06", "补第4周周二", "school"}, "2026-10-10": {true, "2026-10-07", "补第4周周三", "school"}}, HolidayYears: map[string]bool{"2026": true, "2027": false}}
	writeAcademic(t, home, c)
	return c
}
func writeAcademic(t *testing.T, home string, c *AcademicCalendar) {
	t.Helper()
	if e := os.MkdirAll(filepath.Join(home, "assistant"), 0700); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(c)
	if e := os.WriteFile(filepath.Join(home, "assistant", "academic-calendar.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestAcademicSchoolDaysAndUnverifiedAnnualNotice(t *testing.T) {
	c := academicFixture(t, t.TempDir())
	for _, tt := range []struct {
		date                         string
		known, teaching, provisional bool
		source                       string
	}{
		{"2026-08-31", false, false, false, ""}, {"2026-09-13", true, false, false, ""}, {"2026-09-14", true, true, false, "2026-09-14"}, {"2026-09-20", true, true, false, "2026-10-06"}, {"2026-10-06", true, false, false, ""}, {"2026-10-10", true, true, false, "2026-10-07"}, {"2026-11-07", true, false, false, ""}, {"2027-01-01", true, false, true, ""}, {"2027-01-22", true, true, true, "2027-01-22"}, {"2027-01-23", true, false, true, ""}, {"2027-02-28", true, false, true, ""}, {"2027-03-01", true, true, true, "2027-03-01"}, {"2027-07-09", true, true, true, "2027-07-09"}, {"2027-07-10", true, false, true, ""}, {"2027-09-01", false, false, false, ""},
	} {
		at, _ := courseDate(tt.date)
		d := c.Day(at)
		if d.Known != tt.known || d.Teaching != tt.teaching || d.Provisional != tt.provisional || tt.source != "" && d.ClassDate != tt.source {
			t.Errorf("%+v: %+v", tt, d)
		}
	}
}
func TestAcademicResidenceBoundariesAndPersonalPlan(t *testing.T) {
	s := locationFixture(t)
	academicFixture(t, s.Home)
	for _, tt := range []struct{ at, id string }{
		{"2026-11-06T18:59:59+08:00", "school"}, {"2026-11-06T19:00:00+08:00", "home"}, {"2026-11-08T16:59:59+08:00", "home"}, {"2026-11-08T17:00:00+08:00", "school"},
		{"2026-10-09T20:00:00+08:00", "school"}, {"2026-10-10T18:59:59+08:00", "school"}, {"2026-10-10T19:00:00+08:00", "home"}, {"2026-09-19T12:00:00+08:00", "school"},
		{"2026-09-30T19:00:00+08:00", "home"}, {"2027-01-25T12:00:00+08:00", "home"}, {"2027-02-28T17:00:00+08:00", "school"},
	} {
		if got := s.Resolve(timeMust(tt.at)); got.PlaceID != tt.id || got.Source != "academic" {
			t.Errorf("%+v: %+v", tt, got)
		}
	}
	if _, e := s.Apply(t.Context(), LocationRequest{Action: "set_plan", Revision: 1, ID: "trip", PlaceID: "trip", StartsAt: "2026-11-04T00:00:00+08:00", EndsAt: "2026-11-05T00:00:00+08:00"}); e != nil {
		t.Fatal(e)
	}
	if got := s.Resolve(timeMust("2026-11-04T12:00:00+08:00")); got.PlaceID != "trip" {
		t.Fatal(got)
	}
}
func TestAcademicCoursesMapOriginalTeachingWeekAndKeepPersonalExceptions(t *testing.T) {
	s := CourseService{Home: t.TempDir()}
	c := courseFixture(t)
	c.Courses[1].Weeks = []int{4}
	if _, e := s.Apply(t.Context(), CourseRequest{Action: "import", Config: &c}); e != nil {
		t.Fatal(e)
	}
	academicFixture(t, s.Home)
	query := func(date string) CourseDay {
		v, e := s.Apply(t.Context(), CourseRequest{Action: "query", Date: date})
		if e != nil {
			t.Fatal(e)
		}
		return v.(CourseDay)
	}
	d := query("2026-09-20")
	if len(d.Courses) != 1 || d.Courses[0].Week != 4 || d.Courses[0].Date != "2026-09-20" || d.Courses[0].StartsAt.Format("2006-01-02 15:04") != "2026-09-20 08:20" {
		t.Fatal(d)
	}
	if len(query("2026-10-06").Courses) != 0 {
		t.Fatal("holiday classes leaked")
	}
	ex := CourseException{CourseID: "tuesday", Date: "2026-09-20", Cancelled: true}
	if _, e := s.Apply(t.Context(), CourseRequest{Action: "set_exception", Revision: 1, Exception: &ex}); e != nil {
		t.Fatal(e)
	}
	if len(query("2026-09-20").Courses) != 0 {
		t.Fatal("personal makeup cancellation ignored")
	}
	ex = CourseException{CourseID: "monday", Date: "2026-09-28", MoveTo: "2026-10-01", Start: "13:20", End: "14:55"}
	if _, e := s.Apply(t.Context(), CourseRequest{Action: "set_exception", Revision: 2, Exception: &ex}); e != nil {
		t.Fatal(e)
	}
	if d = query("2026-10-01"); len(d.Courses) != 1 || d.Courses[0].OriginalDate != "2026-09-28" {
		t.Fatal("explicit moved-in class lost or duplicated", d)
	}
}
func schoolReminderFixture(t *testing.T) (HeartbeatService, HeartbeatTool) {
	t.Helper()
	s, tool := unifiedFixture(t)
	academicFixture(t, s.Home)
	if e := s.repo().transact(t.Context(), func(tx *sql.Tx) error {
		c, e := loadHeartbeatConfig(t.Context(), tx)
		if e != nil {
			return e
		}
		c.ModuleEnabled["schoolday_reminder"] = true
		c.Triggers = map[string]ModuleTrigger{"uu": {ID: "uu", Title: "UU", Enabled: true, Mode: "daily", Clock: "10:55", Modules: []string{"schoolday_reminder"}, RoutineBound: true, CatchUpSeconds: 120}}
		return saveEntity(t.Context(), tx, "heartbeat_config", heartbeatConfigID, c)
	}); e != nil {
		t.Fatal(e)
	}
	return s, tool
}
func TestAcademicNativeReminderHolidayMakeupLateAndIsolation(t *testing.T) {
	for _, tt := range []struct {
		at   string
		want int
	}{{"2026-11-04T10:54:59+08:00", 0}, {"2026-11-04T10:55:00+08:00", 1}, {"2026-11-07T10:55:00+08:00", 0}, {"2026-09-20T10:55:00+08:00", 1}, {"2026-10-10T10:55:00+08:00", 1}, {"2026-10-06T10:55:00+08:00", 0}, {"2027-01-25T10:55:00+08:00", 0}, {"2026-11-04T10:57:00+08:00", 0}} {
		t.Run(tt.at, func(t *testing.T) {
			s, _ := schoolReminderFixture(t)
			jobs := atHeartbeat(t, s, tt.at)
			if len(jobs) != tt.want {
				t.Fatal(jobs)
			}
			if len(jobs) > 0 && (jobs[0].Kind != "message" || jobs[0].Prompt != "Sir，到时间了，记得用 UU 远程打开宿舍里的电脑。") {
				t.Fatal(jobs)
			}
		})
	}
	s, _ := schoolReminderFixture(t)
	if e := os.Remove(filepath.Join(s.Home, "assistant", "academic-calendar.json")); e != nil {
		t.Fatal(e)
	}
	if len(atHeartbeat(t, s, "2026-11-04T10:55:00+08:00")) != 0 {
		t.Fatal("calendar from another owner used")
	}
}
func TestAcademicReminderDeliveryRestartAndCancellationWithoutModel(t *testing.T) {
	s, _ := schoolReminderFixture(t)
	now := timeMust("2026-11-04T10:55:00+08:00")
	sends := 0
	engine := heartbeat.Engine{Store: JobStore{Repository: s.repo()}, Prepare: s.Prepare, Gate: s.Allows, Sender: courseSender(func() { sends++ }), Clock: func() time.Time { return now }, Runner: mailTestRunner(func(context.Context, heartbeat.Task) (heartbeat.Execution, error) {
		t.Fatal("UU reminder invoked model")
		return heartbeat.Execution{}, nil
	})}
	if e := engine.Tick(t.Context(), now); e != nil {
		t.Fatal(e)
	}
	engine.Prepare = (HeartbeatService{Home: s.Home, Options: s.Options}).Prepare
	if e := engine.Tick(t.Context(), now.Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	if sends != 1 {
		t.Fatal("reminder duplicated", sends)
	}
	s, _ = schoolReminderFixture(t)
	jobs := atHeartbeat(t, s, "2026-11-04T10:55:00+08:00")
	c := academicFixture(t, s.Home)
	c.Adjustments["2026-11-04"] = AcademicAdjustment{Teaching: false, Reason: "停课"}
	writeAcademic(t, s.Home, c)
	if ok, e := s.Allows(t.Context(), jobs[0], now); ok || !errors.Is(e, heartbeat.ErrOccurrenceChanged) {
		t.Fatal("new holiday failed to cancel pending delivery", ok, e)
	}
	s, tool := schoolReminderFixture(t)
	tool.Now = func() time.Time { return timeMust("2026-09-01T12:00:00+08:00") }
	cfg, _ := s.Config(t.Context())
	callHeartbeat(t, tool, fmtHeartbeatOverride(cfg.Revision, "2026-09-20", "11:30"))
	if len(atHeartbeat(t, s, "2026-09-20T10:55:00+08:00")) != 0 {
		t.Fatal("manual sleep override ignored")
	}
}
