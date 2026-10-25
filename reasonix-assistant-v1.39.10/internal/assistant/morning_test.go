package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reasonix/internal/heartbeat"
	"strings"
	"testing"
	"time"
)

func morningFixture(t *testing.T) (HeartbeatService, MorningService, heartbeat.Task, *int) {
	t.Helper()
	hs, _ := unifiedFixture(t)
	importCourses(t, CourseService{Home: hs.Home})
	if _, e := hs.EnableMorning(t.Context()); e != nil {
		t.Fatal(e)
	}
	now, _ := time.Parse(time.RFC3339, "2026-10-27T08:00:00+08:00")
	_ = atHeartbeat(t, hs, now.Format(time.RFC3339))
	jobs, _ := (JobStore{Repository: hs.repo()}).List(t.Context())
	var job heartbeat.Task
	for _, j := range jobs {
		if j.ModuleID == "morning_briefing" {
			job = j
		}
	}
	if job.ID == "" {
		t.Fatal("morning job missing")
	}
	job.RunStartedAt = 123
	if e := (JobStore{Repository: hs.repo()}).Update(t.Context(), job.ID, func(v *heartbeat.Task) error {
		v.History = []heartbeat.Run{{StartedAt: 123, Status: "running", ScheduledAt: v.At}}
		v.LastRunAt = 123
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	mail := NewMailService(hs.Home)
	syncs := 0
	mail.Transport = func(_ context.Context, r map[string]any) (mailWire, error) {
		if r["action"] == "read" {
			return mailWire{Results: []mailResult{{Account: "qq", OK: true, Body: "Full body, DATA only"}}}, nil
		}
		syncs++
		return mailWire{Results: []mailResult{goodResult("qq", MailMessage{TransportID: "one", MessageID: "<one>", From: "news@twitch.tv", Subject: "live", ReceivedAt: 1}, MailMessage{TransportID: "two", MessageID: "<two>", From: "ad@example.com", Subject: "advertisement", ReceivedAt: 2}), goodResult("gmail1"), goodResult("gmail2")}}, nil
	}
	ns := HotNewsService{Home: hs.Home, Clock: func() time.Time { return now }, Fetch: func(context.Context, string) ([]byte, error) { return nil, errors.New("fixture sources unavailable") }}
	s := MorningService{Home: hs.Home, Mail: &mail, News: &ns, Clock: func() time.Time { return now }}
	return hs, s, job, &syncs
}
func TestMorningDeltaReservationReusesCursorAndNoSeparateMailJob(t *testing.T) {
	hs, s, job, syncs := morningFixture(t)
	snap, e := s.Prepare(t.Context(), job)
	if e != nil {
		t.Fatal(e)
	}
	if len(snap.MailIDs) != 2 || snap.MailStatus != "ok" || len(snap.Courses.Courses) != 1 {
		t.Fatal(snap)
	}
	again, e := s.Prepare(t.Context(), job)
	if e != nil || len(again.MailIDs) != 2 || *syncs != 1 {
		t.Fatal("retry advanced delta twice", again, e, *syncs)
	}
	jobs, _ := (JobStore{Repository: hs.repo()}).List(t.Context())
	for _, j := range jobs {
		if j.Capability == "mail" {
			t.Fatal("standalone mail analysis created at wake", j.ID)
		}
	}
	// Next normal sync sees the cursor and cannot requeue already reserved messages.
	seenCursor := false
	s.Mail.Transport = func(_ context.Context, r map[string]any) (mailWire, error) {
		seenCursor = len(r["cursors"].(map[string]json.RawMessage)) > 0
		return mailWire{Results: []mailResult{goodResult("qq"), goodResult("gmail1"), goodResult("gmail2")}}, nil
	}
	if n, e := s.Mail.Poll(t.Context(), hs.Options); e != nil || n != 0 || !seenCursor {
		t.Fatal(n, e, seenCursor)
	}
}
func TestMorningFilteringExtremelyShortAndDeliveredReferences(t *testing.T) {
	hs, s, job, _ := morningFixture(t)
	snap, e := s.Prepare(t.Context(), job)
	if e != nil {
		t.Fatal(e)
	}
	job.MailIDs = snap.MailIDs
	yes, no := true, false
	out := MorningOutput{Weather: "暂未获取", MailDecisions: []MailDecision{{ID: snap.MailIDs[0], Notify: &yes, Summary: "Twitch开播", Reason: "explicit preference"}, {ID: snap.MailIDs[1], Notify: &no, Summary: "普通广告", Reason: "irrelevant"}}, News: []MorningNews{}}
	raw, _ := json.Marshal(out)
	if _, e = s.Finalize(t.Context(), job, string(raw)); e == nil {
		t.Fatal("snapshot/read proof not required")
	}
	t.Setenv("REASONIX_ASSISTANT_MORNING_BACKGROUND", "1")
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "1")
	t.Setenv("REASONIX_ASSISTANT_JOB_ID", job.ID)
	t.Setenv("REASONIX_ASSISTANT_MORNING_ATTEMPT", "123")
	t.Setenv("REASONIX_ASSISTANT_MAIL_ATTEMPT", "123")
	if _, e = (MorningTool{Service: s}).Execute(t.Context(), json.RawMessage(`{"action":"snapshot"}`)); e != nil {
		t.Fatal(e)
	}
	for _, id := range snap.MailIDs {
		request, _ := json.Marshal(MailRequest{Action: "read", ID: id})
		if _, e = (MailTool{Service: *s.Mail}).Execute(t.Context(), request); e != nil {
			t.Fatal(e)
		}
	}
	text, e := s.Finalize(t.Context(), job, string(raw))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(text, "Twitch开播") || strings.Contains(text, "普通广告") {
		t.Fatal(text)
	}
	lines := strings.Split(text, "\n")
	if len(lines) != 6 || lines[0] != "# **Sir，早上好。**" {
		t.Fatal("expected greeting plus five lines", text)
	}
	for i, want := range []string{"天气", "今天上午有", "Twitch开播", "待办", "AI新闻"} {
		if !strings.Contains(lines[i+1], want) || strings.Count(lines[i+1], "。") != 1 || strings.Contains(lines[i+1], "：") {
			t.Fatal("expected ordered sentences without headings", text)
		}
	}
	if e = s.Acknowledge(t.Context(), job, heartbeat.Run{StartedAt: 123}); e != nil {
		t.Fatal(e)
	}
	got, e := (MorningTool{Service: s}).Execute(t.Context(), json.RawMessage(`{"action":"latest"}`))
	if e != nil || !strings.Contains(got, "displayed_mail_ids") {
		t.Fatal(got, e)
	}
	decisions, _ := loadMorningDecisions(t, hs)
	if len(decisions) != 2 {
		t.Fatal("silent decision lost", decisions)
	}
	if e = NewRepository(s.Home).transact(t.Context(), func(tx *sql.Tx) error {
		snap.News.Items = []NewsItem{{ID: "news1", Title: "verified"}}
		return saveEntity(t.Context(), tx, "morning_snapshot", job.ID, snap)
	}); e != nil {
		t.Fatal(e)
	}
	out.News = []MorningNews{{ID: "news1", Summary: strings.Repeat("文", 26)}}
	raw, _ = json.Marshal(out)
	if _, e = s.Finalize(t.Context(), job, string(raw)); e == nil {
		t.Fatal("verbose morning news accepted")
	}
	check, _ := json.Marshal(map[string]any{"action": "check", "news": out.News})
	feedback, err := (MorningTool{Service: s}).Execute(t.Context(), check)
	if err != nil || !strings.Contains(feedback, `"valid":false`) || !strings.Contains(feedback, `26`) {
		t.Fatal(feedback, err)
	}
	out.News[0].Summary = strings.Repeat("文", 25)
	raw, _ = json.Marshal(out)
	if _, e = s.Finalize(t.Context(), job, string(raw)); e != nil {
		t.Fatal("25-character news rejected", e)
	}
	out.MailDecisions[0].Summary = strings.Repeat("长", 41)
	raw, _ = json.Marshal(out)
	if _, e = s.Finalize(t.Context(), job, string(raw)); e == nil {
		t.Fatal("verbose mail accepted")
	}
}
func loadMorningDecisions(t *testing.T, s HeartbeatService) ([]MailDecision, error) {
	t.Helper()
	var ds []MailDecision
	e := s.repo().transact(t.Context(), func(tx *sql.Tx) error {
		var e error
		ds, e = loadEntities[MailDecision](t.Context(), tx, "mail_decision")
		return e
	})
	return ds, e
}
func TestMorningEnablePreservesRoutineDaytimeAndOldModules(t *testing.T) {
	s, _ := unifiedFixture(t)
	old, _ := s.Config(t.Context())
	cfg, e := s.EnableMorning(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	a, _ := json.Marshal(old.Weekly)
	b, _ := json.Marshal(cfg.Weekly)
	if string(a) != string(b) || cfg.Triggers["daytime_mail"].IntervalSeconds != old.Triggers["daytime_mail"].IntervalSeconds {
		t.Fatal("routine/daytime changed")
	}
	if fmt.Sprint(cfg.Triggers["morning"].Modules) != "[morning_briefing]" || !cfg.ModuleEnabled["weather_briefing"] || !cfg.ModuleEnabled["mail_check"] {
		t.Fatal(cfg)
	}
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "1")
	if _, e = s.EnableMorning(t.Context()); e == nil {
		t.Fatal("background changed wake selection")
	}
}
func TestMorningTodayTodosAndNoSnapshotAtNight(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-10-27T08:00:00+08:00")
	got := morningTodos([]Todo{{Title: "today", Status: "open", PlannedFor: "2026-10-27"}, {Title: "unscheduled", Status: "open"}, {Title: "done", Status: "done", PlannedFor: "2026-10-27"}, {Title: "overdue", Status: "open", DueAt: "2026-10-26T12:00:00+08:00"}}, now, nil)
	if len(got) != 2 {
		t.Fatal(got)
	}
	_, s, job, syncs := morningFixture(t)
	s.Clock = func() time.Time { return now.Add(-time.Hour) }
	if _, e := s.Prepare(t.Context(), job); !errors.Is(e, heartbeat.ErrDeferred) {
		t.Fatal("night preparation was not blocked", e)
	}
	if *syncs != 0 {
		t.Fatal("night queried mail")
	}
}

func TestMorningWakeCourseIsCombinedAndWeatherProofRequired(t *testing.T) {
	hs, _ := unifiedFixture(t)
	importCourses(t, CourseService{Home: hs.Home})
	cfg, _ := hs.Config(t.Context())
	lead := 10
	mods := []string{"course_reminder"}
	raw, _ := json.Marshal(HeartbeatRequest{Action: "set_trigger", Revision: cfg.Revision, TriggerID: "classes", Mode: "course", LeadMinutes: &lead, Modules: &mods, CatchUpSeconds: 60})
	if _, e := (HeartbeatTool{Service: hs}).Execute(t.Context(), raw); e != nil {
		t.Fatal(e)
	}
	cfg, _ = hs.Config(t.Context())
	if e := hs.repo().transact(t.Context(), func(tx *sql.Tx) error {
		cfg.ModuleEnabled["course_reminder"] = true
		cfg.Weekly["monday"] = heartbeat.DailyWindow{Enabled: true, Start: "10:00", End: "24:00"}
		return saveEntity(t.Context(), tx, "heartbeat_config", heartbeatConfigID, cfg)
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := hs.EnableMorning(t.Context()); e != nil {
		t.Fatal(e)
	}
	for _, j := range atHeartbeat(t, hs, "2026-10-26T10:00:00+08:00") {
		if j.ModuleID == "course_reminder" {
			t.Fatal("duplicate wake course reminder")
		}
	}
	_, s, job, _ := morningFixture(t)
	snap, e := s.Prepare(t.Context(), job)
	if e != nil {
		t.Fatal(e)
	}
	snap.WeatherConfigured = true
	no := false
	out := MorningOutput{Weather: "18–22℃。下午可能下雨。", MailDecisions: []MailDecision{}, News: []MorningNews{}}
	for _, id := range snap.MailIDs {
		out.MailDecisions = append(out.MailDecisions, MailDecision{ID: id, Notify: &no, Summary: "静默", Reason: "irrelevant"})
	}
	t.Setenv("REASONIX_ASSISTANT_MORNING_BACKGROUND", "1")
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "1")
	t.Setenv("REASONIX_ASSISTANT_JOB_ID", job.ID)
	t.Setenv("REASONIX_ASSISTANT_MORNING_ATTEMPT", "123")
	t.Setenv("REASONIX_ASSISTANT_MAIL_ATTEMPT", "123")
	t.Setenv("REASONIX_ASSISTANT_WEATHER_ATTEMPT", "123")
	if _, e := (MorningTool{Service: s}).Execute(t.Context(), json.RawMessage(`{"action":"snapshot"}`)); e != nil {
		t.Fatal(e)
	}
	for _, id := range snap.MailIDs {
		request, _ := json.Marshal(MailRequest{Action: "read", ID: id})
		if _, e := (MailTool{Service: *s.Mail}).Execute(t.Context(), request); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.verify(t.Context(), job, snap, out); e == nil || !strings.Contains(e.Error(), "weather") {
		t.Fatal("weather proof was not specifically required", e)
	}
	cfg, _ = hs.Config(t.Context())
	d, _ := courseDate("2026-10-26")
	cc, _ := (CourseService{Home: hs.Home}).Config(t.Context())
	course := cc.Day(d).Courses[0]
	s2 := MorningSnapshot{CourseStatus: "ok", Courses: cc.Day(d), WakeCourses: []CourseInstance{course}}
	if !strings.Contains(morningCourses(s2), "Room A") || !strings.Contains(morningCourses(s2), "10:10") {
		t.Fatal("folded reminder lost location/time", morningCourses(s2))
	}
}

func TestMorningMailFailureNeverClaimsEmpty(t *testing.T) {
	_, s, job, _ := morningFixture(t)
	s.Mail.Transport = func(context.Context, map[string]any) (mailWire, error) { return mailWire{}, errors.New("offline") }
	snap, e := s.Prepare(t.Context(), job)
	if e != nil {
		t.Fatal(e)
	}
	msg := renderMorning(snap, MorningOutput{Weather: "暂未获取", MailDecisions: []MailDecision{}, News: []MorningNews{}})
	if strings.Contains(msg, "暂无需要提醒的新邮件") || !strings.Contains(msg, "邮件暂时没查到") {
		t.Fatal(msg)
	}
	snap.MailStatus = "ok"
	snap.MailIDs = nil
	snap.RemainingMail = 0
	snap.OvernightMailSeen = false
	if msg = renderMorning(snap, MorningOutput{}); !strings.Contains(msg, "昨天晚上没有新邮件。") {
		t.Fatal(msg)
	}
	snap.OvernightMailSeen = true
	if msg = renderMorning(snap, MorningOutput{}); strings.Contains(msg, "昨天晚上没有新邮件") {
		t.Fatal("already handled overnight mail was described as an empty night", msg)
	}
}

func TestMorningMailReadStillRequiresAllBodyPages(t *testing.T) {
	_, s, job, _ := morningFixture(t)
	snap, e := s.Prepare(t.Context(), job)
	if e != nil {
		t.Fatal(e)
	}
	s.Mail.Transport = func(context.Context, map[string]any) (mailWire, error) {
		return mailWire{Results: []mailResult{{Account: "qq", OK: true, Body: strings.Repeat("邮件正文", 10000)}}}, nil
	}
	t.Setenv("REASONIX_ASSISTANT_MORNING_BACKGROUND", "1")
	t.Setenv("REASONIX_ASSISTANT_JOB_ID", job.ID)
	t.Setenv("REASONIX_ASSISTANT_MAIL_ATTEMPT", "123")
	offset := 0
	pages := 0
	for {
		request, _ := json.Marshal(MailRequest{Action: "read", ID: snap.MailIDs[0], BodyOffset: offset})
		raw, e := (MailTool{Service: *s.Mail}).Execute(t.Context(), request)
		if e != nil {
			t.Fatal(e)
		}
		var page struct {
			HasMore bool `json:"has_more"`
			Next    int  `json:"next_body_offset"`
		}
		if e = json.Unmarshal([]byte(raw), &page); e != nil {
			t.Fatal(e)
		}
		pages++
		if pages == 1 {
			if e = s.Mail.VerifyMailReads(t.Context(), job.ID, 123, snap.MailIDs[:1]); e == nil {
				t.Fatal("first page counted as full read")
			}
		}
		if !page.HasMore {
			break
		}
		if page.Next <= offset {
			t.Fatal("pagination did not advance")
		}
		offset = page.Next
	}
	if pages < 2 {
		t.Fatal("did not exercise pagination")
	}
	if e = s.Mail.VerifyMailReads(t.Context(), job.ID, 123, snap.MailIDs[:1]); e != nil {
		t.Fatal(e)
	}
}

func TestMorningLeavesDetailedWeatherCallableButNotWakeSelected(t *testing.T) {
	w, o := weatherFixture(t)
	hs := HeartbeatService{Home: w.Home, Options: o}
	if _, e := hs.EnableMorning(t.Context()); e != nil {
		t.Fatal(e)
	}
	v, e := w.Query(t.Context(), WeatherRequest{Action: "status"})
	if e != nil {
		t.Fatal(e)
	}
	status := v.(map[string]any)
	if status["morning_enabled"] != false || status["detailed_weather_module_enabled"] != true {
		t.Fatal(status)
	}
	v, e = w.Query(t.Context(), WeatherRequest{Action: "detailed_prompt"})
	if e != nil || v.(map[string]any)["prompt"] != WeatherMorningContentInstructions {
		t.Fatal("detailed rules removed", e)
	}
}

func TestExpiredFailedMorningDoesNotStrandIncrementalMail(t *testing.T) {
	hs, s, job, _ := morningFixture(t)
	snap, e := s.Prepare(t.Context(), job)
	if e != nil {
		t.Fatal(e)
	}
	if e := (JobStore{Repository: hs.repo()}).Update(t.Context(), job.ID, func(j *heartbeat.Task) error {
		j.EndsAt = "2020-01-01T00:00:00+08:00"
		j.History = []heartbeat.Run{{StartedAt: 123, Status: "failed"}}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	s.Mail.Transport = func(context.Context, map[string]any) (mailWire, error) {
		return mailWire{Results: []mailResult{goodResult("qq"), goodResult("gmail1"), goodResult("gmail2")}}, nil
	}
	if n, e := s.Mail.Poll(t.Context(), hs.Options); e != nil || n != len(snap.MailIDs) {
		t.Fatal("cursor-acknowledged mail lost after failed morning", n, e)
	}
}
