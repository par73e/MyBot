package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reasonix/internal/heartbeat"
	"strings"
	"testing"
	"time"
)

func courseFixture(t *testing.T) CourseConfig {
	t.Helper()
	return CourseConfig{Name: "Test term", FirstWeekMonday: "2026-09-14", TotalWeeks: 16, Timezone: "Asia/Shanghai", Periods: []CoursePeriod{{1, "08:20", "09:05"}, {2, "09:10", "09:55"}, {3, "10:10", "10:55"}, {4, "11:00", "11:45"}, {5, "11:50", "12:30"}}, Courses: []CourseDefinition{{ID: "monday", Title: "Test Monday", Weekday: 1, Periods: []int{3, 4, 5}, Room: "Room A"}, {ID: "tuesday", Title: "Test Tuesday", Weekday: 2, Periods: []int{1, 2}, Room: "Room B"}}}
}
func importCourses(t *testing.T, s CourseService) CourseConfig {
	t.Helper()
	c := courseFixture(t)
	v, e := s.Apply(t.Context(), CourseRequest{Action: "import", Config: &c})
	if e != nil {
		t.Fatal(e)
	}
	return v.(CourseConfig)
}
func TestCourseCivilDatesWeeksAndCombinedPeriods(t *testing.T) {
	s := CourseService{Home: t.TempDir()}
	importCourses(t, s)
	for _, tt := range []struct {
		date        string
		week, count int
		inTerm      bool
	}{{"2026-10-25", 6, 0, true}, {"2026-10-26", 7, 1, true}, {"2026-10-27", 7, 1, true}, {"2027-01-03", 16, 0, true}, {"2027-01-04", 17, 0, false}, {"2026-09-13", 0, 0, false}} {
		v, e := s.Apply(t.Context(), CourseRequest{Action: "query", Date: tt.date})
		if e != nil {
			t.Fatal(e)
		}
		d := v.(CourseDay)
		if d.Week != tt.week || len(d.Courses) != tt.count || d.InTerm != tt.inTerm {
			t.Fatal(tt, d)
		}
		if tt.date == "2026-10-26" && (d.Courses[0].StartsAt.Format("15:04") != "10:10" || d.Courses[0].EndsAt.Format("15:04") != "12:30") {
			t.Fatal(d)
		}
	}
	s.Clock = func() time.Time { v, _ := time.Parse(time.RFC3339, "2026-10-26T05:30:00+08:00"); return v }
	v, e := s.Apply(t.Context(), CourseRequest{Action: "query"})
	if e != nil || v.(CourseDay).Date != "2026-10-26" {
		t.Fatal("06:00 chat boundary leaked into courses", v, e)
	}
}
func TestCourseExceptionsRevisionAndRestart(t *testing.T) {
	s := CourseService{Home: t.TempDir()}
	importCourses(t, s)
	ex := CourseException{CourseID: "tuesday", Date: "2026-10-27", MoveTo: "2026-10-28", Start: "13:20", End: "14:55", Room: "Room C"}
	if _, e := s.Apply(t.Context(), CourseRequest{Action: "set_exception", Revision: 1, Exception: &ex}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Apply(t.Context(), CourseRequest{Action: "set_exception", Revision: 1, Exception: &ex}); e == nil {
		t.Fatal("stale revision accepted")
	}
	restored := CourseService{Home: s.Home}
	a, _ := restored.Apply(t.Context(), CourseRequest{Action: "query", Date: "2026-10-27"})
	b, _ := restored.Apply(t.Context(), CourseRequest{Action: "query", Date: "2026-10-28"})
	if len(a.(CourseDay).Courses) != 0 || len(b.(CourseDay).Courses) != 1 || b.(CourseDay).Courses[0].Room != "Room C" || b.(CourseDay).Courses[0].ID != "tuesday:2026-10-27" {
		t.Fatal(a, b)
	}
	if _, e := s.Apply(t.Context(), CourseRequest{Action: "remove_exception", Revision: 2, CourseID: "tuesday", Date: "2026-10-27"}); e != nil {
		t.Fatal(e)
	}
	ex = CourseException{CourseID: "tuesday", Date: "2026-10-27", Cancelled: true}
	if _, e := s.Apply(t.Context(), CourseRequest{Action: "set_exception", Revision: 3, Exception: &ex}); e != nil {
		t.Fatal(e)
	}
	a, _ = s.Apply(t.Context(), CourseRequest{Action: "query", Date: "2026-10-27"})
	if len(a.(CourseDay).Courses) != 0 {
		t.Fatal(a)
	}
}
func TestCourseTeachingWeeksAndValidation(t *testing.T) {
	c := courseFixture(t)
	c.Revision = 1
	c.Courses[0].Weeks = []int{6}
	day, _ := courseDate("2026-10-26")
	if len(c.Day(day).Courses) != 0 {
		t.Fatal("week restriction ignored")
	}
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	c.Courses[0].Weeks = []int{17}
	if e := c.Validate(); e == nil {
		t.Fatal("out of term week accepted")
	}
	c = courseFixture(t)
	c.Revision = 1
	c.Courses[0].Periods = []int{3, 5}
	if e := c.Validate(); e == nil {
		t.Fatal("discontinuous block silently merged")
	}
}
func TestCourseToolBackgroundAndMissingData(t *testing.T) {
	s := CourseService{Home: t.TempDir()}
	tool := CourseTool{Service: s}
	raw, e := tool.Execute(t.Context(), json.RawMessage(`{"action":"query"}`))
	if e != nil || !strings.Contains(raw, `"configured":false`) {
		t.Fatal(raw, e)
	}
	importCourses(t, s)
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "1")
	if _, e = tool.Execute(t.Context(), json.RawMessage(`{"action":"query","date":"2026-10-26"}`)); e != nil {
		t.Fatal(e)
	}
	if _, e = tool.Execute(t.Context(), json.RawMessage(`{"action":"remove_exception","revision":1,"date":"2026-10-26","course_id":"monday"}`)); e == nil {
		t.Fatal("background changed courses")
	}
	if _, e = tool.Execute(t.Context(), json.RawMessage(`{"action":"query","path":"/etc/passwd"}`)); e == nil {
		t.Fatal("unknown field accepted")
	}
}
func setupCourseHeartbeat(t *testing.T) (HeartbeatService, HeartbeatTool) {
	t.Helper()
	s, tool := unifiedFixture(t)
	importCourses(t, CourseService{Home: s.Home})
	c, _ := s.Config(t.Context())
	raw, _ := json.Marshal(HeartbeatRequest{Action: "set_module_enabled", Revision: c.Revision, ModuleID: "course_reminder", Enabled: boolPtr(true)})
	if _, e := tool.Execute(t.Context(), raw); e != nil {
		t.Fatal(e)
	}
	c, _ = s.Config(t.Context())
	lead := 10
	mods := []string{"course_reminder"}
	raw, _ = json.Marshal(HeartbeatRequest{Action: "set_trigger", Revision: c.Revision, TriggerID: "courses", Mode: "course", Modules: &mods, LeadMinutes: &lead, CatchUpSeconds: 60})
	if _, e := tool.Execute(t.Context(), raw); e != nil {
		t.Fatal(e)
	}
	return s, tool
}
func boolPtr(v bool) *bool { return &v }
func courseJobs(t *testing.T, s HeartbeatService, at string) []heartbeat.Task {
	t.Helper()
	out := []heartbeat.Task{}
	for _, j := range atHeartbeat(t, s, at) {
		if j.ModuleID == "course_reminder" {
			out = append(out, j)
		}
	}
	return out
}
func TestCourseReminderTopLevelWindowAndDedupe(t *testing.T) {
	s, tool := setupCourseHeartbeat(t)
	if n := len(courseJobs(t, s, "2026-10-26T09:59:59+08:00")); n != 0 {
		t.Fatal(n)
	}
	jobs := courseJobs(t, s, "2026-10-26T10:00:00+08:00")
	if len(jobs) != 1 || jobs[0].Kind != "message" || jobs[0].At != "2026-10-26T10:00:00+08:00" {
		t.Fatal(jobs)
	}
	if n := len(courseJobs(t, s, "2026-10-26T10:00:01+08:00")); n != 1 {
		t.Fatal("duplicate", n)
	}
	c, _ := s.Config(t.Context())
	callHeartbeat(t, tool, fmtHeartbeatOverride(c.Revision, "2026-10-27", "09:00"))
	if n := len(courseJobs(t, s, "2026-10-27T09:00:00+08:00")); n != 1 {
		t.Fatal("08:10 Tuesday event escaped 09:00 window", n)
	} // Only Monday's unclaimed job remains in the list.
	now, _ := time.Parse(time.RFC3339, "2026-10-26T10:00:01+08:00")
	ex := CourseException{CourseID: "monday", Date: "2026-10-26", Cancelled: true}
	if _, e := (CourseService{Home: s.Home}).Apply(t.Context(), CourseRequest{Action: "set_exception", Revision: 1, Exception: &ex}); e != nil {
		t.Fatal(e)
	}
	if ok, e := s.Allows(t.Context(), jobs[0], now); ok || !errors.Is(e, heartbeat.ErrOccurrenceChanged) {
		t.Fatal("cancelled class remained sendable", ok, e)
	}
}
func fmtHeartbeatOverride(rev int, date, start string) string {
	raw, _ := json.Marshal(HeartbeatRequest{Action: "set_date_override", Revision: rev, Date: date, Start: start})
	return string(raw)
}
func TestGlobalRoutineSkipsFixedReminderAndRecordsReason(t *testing.T) {
	s, _ := unifiedFixture(t)
	now, _ := time.Parse(time.RFC3339, "2026-10-27T09:00:00+08:00")
	if err := s.repo().transact(t.Context(), func(tx *sql.Tx) error {
		c, e := loadHeartbeatConfig(t.Context(), tx)
		if e != nil {
			return e
		}
		c.Overrides["2026-10-27"] = HeartbeatOverride{Start: "09:00"}
		return saveEntity(t.Context(), tx, "heartbeat_config", heartbeatConfigID, c)
	}); err != nil {
		t.Fatal(err)
	}
	tasks := Tasks{Repository: s.repo(), Options: s.Options, Now: func() time.Time { return now.Add(-2 * time.Hour) }}
	v := createTask(t, tasks, TaskRequest{Title: "early", At: "2026-10-27T08:00:00+08:00"})
	sends := 0
	e := heartbeat.Engine{Store: JobStore{Repository: s.repo()}, Gate: s.Allows, Sender: courseSender(func() { sends++ }), Clock: func() time.Time { return now }}
	if err := e.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	jobs, _ := (JobStore{Repository: s.repo()}).List(t.Context())
	for _, j := range jobs {
		if j.ID == v.Schedule.ID {
			if j.Enabled || len(j.History) != 1 || j.History[0].Status != "skipped" {
				t.Fatal(j)
			}
		}
	}
	if sends != 0 {
		t.Fatal("outside routine reminder sent")
	}
}

type courseSender func()

func (s courseSender) Send(context.Context, heartbeat.Target, string) error { s(); return nil }

func TestCourseReminderEngineDeliveryAndRestartWithoutAI(t *testing.T) {
	s, _ := setupCourseHeartbeat(t)
	if err := s.repo().transact(t.Context(), func(tx *sql.Tx) error {
		c, e := loadHeartbeatConfig(t.Context(), tx)
		if e != nil {
			return e
		}
		c.Triggers = map[string]ModuleTrigger{"courses": c.Triggers["courses"]}
		return saveEntity(t.Context(), tx, "heartbeat_config", heartbeatConfigID, c)
	}); err != nil {
		t.Fatal(err)
	}
	now, _ := time.Parse(time.RFC3339, "2026-10-26T10:00:00+08:00")
	sends := 0
	e := heartbeat.Engine{Store: JobStore{Repository: s.repo()}, Prepare: s.Prepare, Gate: s.Allows, Sender: courseSender(func() { sends++ }), Clock: func() time.Time { return now }, Runner: mailTestRunner(func(context.Context, heartbeat.Task) (heartbeat.Execution, error) {
		t.Fatal("native course reminder called AI")
		return heartbeat.Execution{}, nil
	})}
	if err := e.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	jobs, _ := (JobStore{Repository: s.repo()}).List(t.Context())
	found := 0
	for _, j := range jobs {
		if j.ModuleID != "course_reminder" {
			continue
		}
		found++
		if j.Enabled || len(j.History) != 1 || j.History[0].Status != "delivered" || !strings.Contains(j.History[0].Result, "Room A") {
			t.Fatal(j)
		}
	}
	restored := HeartbeatService{Home: s.Home, Options: s.Options}
	e.Prepare = restored.Prepare
	e.Gate = restored.Allows
	now = now.Add(10 * time.Second)
	if err := e.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if sends != 1 || found != 1 {
		t.Fatal("delivery duplicated or missing", sends, found)
	}
}

func TestGlobalRoutineScheduledBoundariesAndPending(t *testing.T) {
	s, _ := unifiedFixture(t)
	c, _ := s.Config(t.Context())
	c.Overrides["2026-10-27"] = HeartbeatOverride{Start: "09:00", End: "18:00"}
	for _, tt := range []struct {
		event, now string
		want       bool
	}{{"08:00", "09:00", false}, {"09:00", "09:00", true}, {"10:00", "10:00", true}, {"18:00", "18:00", false}, {"17:59", "18:00", false}} {
		at, _ := time.Parse(time.RFC3339, "2026-10-27T"+tt.event+":00+08:00")
		now, _ := time.Parse(time.RFC3339, "2026-10-27T"+tt.now+":00+08:00")
		for _, kind := range []string{"message", "agent", "mail_check", "weather_check"} {
			ok, _ := routineAdmission(c, heartbeat.Task{Kind: kind, At: at.Format(time.RFC3339)}, now)
			if ok != tt.want {
				t.Fatal(tt, kind, ok)
			}
		}
	}
	if err := s.repo().transact(t.Context(), func(tx *sql.Tx) error { return saveEntity(t.Context(), tx, "heartbeat_config", heartbeatConfigID, c) }); err != nil {
		t.Fatal(err)
	}
	outside, _ := time.Parse(time.RFC3339, "2026-10-27T08:00:00+08:00")
	if err := s.CheckRunNotification(t.Context(), "untracked", outside); !errors.Is(err, heartbeat.ErrOutsideRoutine) {
		t.Fatal("untracked sender bypassed routine", err)
	}
}

func TestCourseBriefingRequiresFreshActualRead(t *testing.T) {
	s := CourseService{Home: t.TempDir()}
	importCourses(t, s)
	now, _ := time.Parse(time.RFC3339, "2026-10-26T10:00:00+08:00")
	s.Clock = func() time.Time { return now }
	task := heartbeat.Task{ID: "briefing", At: now.Format(time.RFC3339), RunStartedAt: 7}
	good := `{"notify":true,"message":"Sir, Test Monday at Room A","course_ids":["monday:2026-10-26"]}`
	if e := s.VerifyBriefing(t.Context(), task, good); e == nil {
		t.Fatal("model invented a briefing without a query")
	}
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "1")
	t.Setenv("REASONIX_ASSISTANT_COURSE_BACKGROUND", "1")
	t.Setenv("REASONIX_ASSISTANT_JOB_ID", task.ID)
	t.Setenv("REASONIX_ASSISTANT_COURSE_ATTEMPT", "7")
	if _, e := (CourseTool{Service: s}).Execute(t.Context(), json.RawMessage(`{"action":"query","date":"2026-10-26"}`)); e != nil {
		t.Fatal(e)
	}
	if e := s.VerifyBriefing(t.Context(), task, good); e != nil {
		t.Fatal(e)
	}
	if e := s.VerifyBriefing(t.Context(), task, `{"notify":true,"message":"invented","course_ids":["made-up"]}`); e == nil {
		t.Fatal("invented courses accepted")
	}
	task.History = []heartbeat.Run{{StartedAt: 7, Status: "delivery_pending"}}
	if ok, e := s.briefingCurrent(t.Context(), task); e != nil || !ok {
		t.Fatal(ok, e)
	}
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "")
	ex := CourseException{CourseID: "monday", Date: "2026-10-26", Room: "Room D"}
	if _, e := s.Apply(t.Context(), CourseRequest{Action: "set_exception", Revision: 1, Exception: &ex}); e != nil {
		t.Fatal(e)
	}
	if ok, e := s.briefingCurrent(t.Context(), task); ok || !errors.Is(e, heartbeat.ErrOccurrenceChanged) {
		t.Fatal("stale room survived", ok, e)
	}
}

func TestCourseModuleCannotBreakOrdinaryTriggerPlanning(t *testing.T) {
	_, tool := setupCourseHeartbeat(t)
	cfg, _ := tool.Service.Config(t.Context())
	for _, args := range []string{
		`{"action":"set_trigger","trigger_id":"morning","weekday":"monday","modules":["course_reminder"]}`,
		`{"action":"set_date_override","date":"2026-10-26","trigger_id":"morning","modules":["course_reminder"]}`,
		`{"action":"set_trigger","trigger_id":"courses","weekday":"monday","modules":["weather_briefing"]}`,
		`{"action":"set_trigger","trigger_id":"courses","routine_bound":false}`,
	} {
		var obj map[string]any
		_ = json.Unmarshal([]byte(args), &obj)
		obj["revision"] = cfg.Revision
		raw, _ := json.Marshal(obj)
		if _, e := tool.Execute(t.Context(), raw); e == nil {
			t.Fatal("invalid module/trigger combination accepted", args)
		}
	}
}
