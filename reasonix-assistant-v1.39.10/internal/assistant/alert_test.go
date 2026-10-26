package assistant

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"reasonix/internal/heartbeat"
)

func TestAlertIndependentFastSourceDoesNotWaitForSlowSource(t *testing.T) {
	hs, s, f := newAlertFixture(t)
	a := fixtureOfficialAlert(t, f.now.Add(-time.Minute), "黄色")
	f.sh = []map[string]string{f.shAlert(a)}
	blocked := make(chan struct{})
	f.nmcBlock = blocked
	done := make(chan error, 1)
	go func() { done <- s.Poll(t.Context(), hs.Options) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(alertEventsTest(t, s)) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	events := alertEventsTest(t, s)
	if len(events) != 1 || events[0].JobID == "" {
		close(blocked)
		<-done
		t.Fatal("a slow source withheld a fast official warning", events)
	}
	sent := 0
	e := alertEngine(hs, s, func() { sent++ })
	if err := e.Tick(t.Context(), f.now); err != nil || sent != 1 {
		close(blocked)
		<-done
		t.Fatal("queued warning waited for consensus", sent, err)
	}
	close(blocked)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestAlertLaggingSourceCannotDowngradeAndCrossSourceDeduplicates(t *testing.T) {
	hs, s, f := newAlertFixture(t)
	blue := fixtureOfficialAlert(t, f.now.Add(-2*time.Minute), "蓝色")
	if err := s.ingest(t.Context(), blue, hs.Options, 1); err != nil {
		t.Fatal(err)
	}
	sent := 0
	e := alertEngine(hs, s, func() { sent++ })
	if err := e.Tick(t.Context(), f.now); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Minute)
	orange := fixtureOfficialAlert(t, f.now.Add(-time.Second), "橙色")
	if err := s.ingest(t.Context(), orange, hs.Options, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.ingest(t.Context(), blue, hs.Options, 1); err != nil {
		t.Fatal(err)
	}
	orange.Source = "weathercn"
	if err := s.ingest(t.Context(), orange, hs.Options, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.Tick(t.Context(), f.now); err != nil || sent != 2 {
		t.Fatal("upgrade was suppressed or repeated", sent, err)
	}
	if err := e.Tick(t.Context(), f.now); err != nil || sent != 2 {
		t.Fatal("restart/recheck replayed delivered warning", sent, err)
	}
	r, err := alertRead[AlertRecord](t.Context(), s.repo(), "alert_record", orange.Key)
	if err != nil || r.Alert.Rank != 3 {
		t.Fatal("lagging source downgraded record", r, err)
	}
}
func TestAlertEmptySourcesDoNotCancelExplicitCancellationDoes(t *testing.T) {
	hs, s, f := newAlertFixture(t)
	a := fixtureOfficialAlert(t, f.now.Add(-time.Minute), "黄色")
	if err := s.ingest(t.Context(), a, hs.Options, 1); err != nil {
		t.Fatal(err)
	}
	sent := 0
	e := alertEngine(hs, s, func() { sent++ })
	if err := e.Tick(t.Context(), f.now); err != nil {
		t.Fatal(err)
	}
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	if len(alertEventsTest(t, s)) != 1 {
		t.Fatal("empty source invented cancellation")
	}
	a.Cancel = true
	a.PublishedAt = f.now
	a.Body = "浦东新区气象台解除大风黄色预警信号。"
	if err := s.ingest(t.Context(), a, hs.Options, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.Tick(t.Context(), f.now); err != nil || sent != 2 {
		t.Fatal("explicit cancellation was lost", sent, err)
	}
}
func TestAlertExpiredOldAndSupersededMessagesCannotLeak(t *testing.T) {
	hs, s, f := newAlertFixture(t)
	a := fixtureOfficialAlert(t, f.now.Add(-time.Minute), "黄色")
	expired := a
	expired.ExpiresAt = f.now.Add(-time.Second)
	if err := s.ingest(t.Context(), expired, hs.Options, 1); err != nil || len(alertEventsTest(t, s)) != 0 {
		t.Fatal("expired warning queued", err)
	}
	if err := s.ingest(t.Context(), a, hs.Options, 1); err != nil {
		t.Fatal(err)
	}
	a.Level = "橙色"
	a.Rank = 3
	a.PublishedAt = f.now
	if err := s.ingest(t.Context(), a, hs.Options, 1); err != nil {
		t.Fatal(err)
	}
	sent := 0
	e := alertEngine(hs, s, func() { sent++ })
	if err := e.Tick(t.Context(), f.now); err != nil || sent != 1 {
		t.Fatal("superseded pending warning leaked", sent, err)
	}
	for _, ev := range alertEventsTest(t, s) {
		if ev.Alert.Level == "黄色" && !ev.Superseded {
			t.Fatal("old event not superseded")
		}
	}
}
func TestAlertNativeWindowOffAndRevisionGuards(t *testing.T) {
	hs, s, f := newAlertFixture(t)
	before, _ := hs.Config(t.Context())
	no := false
	if _, err := s.Apply(t.Context(), AlertRequest{Action: "configure", Revision: 1, Enabled: &no}); err != nil {
		t.Fatal(err)
	}
	after, _ := hs.Config(t.Context())
	if len(before.Weekly) != len(after.Weekly) || before.Weekly["monday"] != after.Weekly["monday"] {
		t.Fatal("alert pause changed routine")
	}
	if err := s.Poll(t.Context(), hs.Options); err != nil || f.requests != 0 {
		t.Fatal("disabled module polled", err)
	}
	a := fixtureOfficialAlert(t, f.now.Add(-time.Minute), "黄色")
	if err := s.ingest(t.Context(), a, hs.Options, 1); err != nil || len(alertEventsTest(t, s)) != 0 {
		t.Fatal("old config committed after pause", err)
	}
	yes := true
	if _, err := s.Apply(t.Context(), AlertRequest{Action: "configure", Revision: 2, Enabled: &yes}); err != nil {
		t.Fatal(err)
	}
	f.now = timeMust("2026-10-27T07:00:00+08:00")
	if err := s.Poll(t.Context(), hs.Options); err != nil || f.requests != 0 {
		t.Fatal("outside routine polled", err)
	}
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "1")
	if _, err := s.Apply(t.Context(), AlertRequest{Action: "configure", Revision: 3, Enabled: &no}); err == nil {
		t.Fatal("external background configured tracking")
	}
}
func TestAlertHTTP304BackoffAndMalformedResponseRemainObservable(t *testing.T) {
	hs, s, f := newAlertFixture(t)
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(5 * time.Minute)
	if err := s.Poll(t.Context(), hs.Options); err != nil || f.notModified == 0 {
		t.Fatal("304 caching failed", err)
	}
	f.fail["shweather"] = 429
	f.now = f.now.Add(5 * time.Minute)
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	h, err := alertRead[AlertHealth](t.Context(), s.repo(), "alert_health", "shanghai")
	if err != nil || h.Failures != 1 || h.Error == "" {
		t.Fatal("429 became no warning", h, err)
	}
	cached, _ := alertRead[alertCache](t.Context(), s.repo(), "alert_http", mailHash(alertShanghai))
	if cached.BlockedUntil.Sub(f.now) != 20*time.Minute {
		t.Fatal("Retry-After was ignored", cached)
	}
	if _, err = s.fetch(t.Context(), "https://attacker.test/alarm", "https://www.nmc.cn"); err == nil {
		t.Fatal("arbitrary URL permitted")
	}
}
func TestAlertDeliveryUncertaintyDoesNotAutoReplay(t *testing.T) {
	hs, s, f := newAlertFixture(t)
	a := fixtureOfficialAlert(t, f.now.Add(-time.Minute), "黄色")
	if err := s.ingest(t.Context(), a, hs.Options, 1); err != nil {
		t.Fatal(err)
	}
	sent := 0
	e := alertEngine(hs, s, func() { sent++ })
	e.Sender = alertErrorSender{err: heartbeat.ErrDeliveryUncertain}
	_ = e.Tick(t.Context(), f.now)
	e.Sender = courseSender(func() { sent++ })
	if err := s.reconcileAlerts(t.Context(), hs.Options, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.Tick(t.Context(), f.now); err != nil || sent != 0 {
		t.Fatal("uncertain delivery was replayed", sent, err)
	}
	status, err := s.Status(t.Context())
	if err != nil || len(status.(map[string]any)["delivery_issues"].([]map[string]string)) != 1 {
		t.Fatal("uncertainty not exposed", status, err)
	}
}

type alertErrorSender struct{ err error }

func (s alertErrorSender) Send(context.Context, heartbeat.Target, string) error { return s.err }

func TestAlertNativeHeartbeatNeverInvokesModel(t *testing.T) {
	hs, s, f := newAlertFixture(t)
	a := fixtureOfficialAlert(t, f.now.Add(-time.Minute), "黄色")
	f.sh = []map[string]string{f.shAlert(a)}
	sent, native := 0, 0
	e := alertEngine(hs, s, func() { sent++ })
	e.Prepare = hs.Prepare
	e.Checker = mailTestRunner(func(ctx context.Context, t heartbeat.Task) (heartbeat.Execution, error) {
		if t.ModuleID == "weather_alert_check" {
			native++
			return heartbeat.Execution{}, s.Poll(ctx, hs.Options)
		}
		return heartbeat.Execution{}, nil
	})
	f.now = f.now.Add(5 * time.Minute)
	if err := e.Tick(t.Context(), f.now); err != nil {
		t.Fatal(err)
	}
	if err := e.Tick(t.Context(), f.now); err != nil || native != 1 || sent != 1 {
		t.Fatal("native route/once-only notification failed", native, sent, err)
	}
}
func TestAlertAllSourcesFailedHealthNoticeAndRecovery(t *testing.T) {
	hs, s, f := newAlertFixture(t)
	f.fail["/"] = 503
	for _, delay := range []time.Duration{0, 5 * time.Minute, 10 * time.Minute} {
		f.now = f.now.Add(delay)
		if err := s.Poll(t.Context(), hs.Options); err != nil {
			t.Fatal(err)
		}
	}
	sent := 0
	e := alertEngine(hs, s, func() { sent++ })
	if err := e.Tick(t.Context(), f.now); err != nil || sent != 1 {
		t.Fatal("complete outage did not produce a factual notice", sent, err)
	}
	f.fail = map[string]int{}
	f.now = f.now.Add(20 * time.Minute)
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Status(t.Context())
	if v.(map[string]any)["coverage"] != "healthy" {
		t.Fatal("recovery not observable", v)
	}
}
func TestAlertNoFalseCancellationFromDefenceText(t *testing.T) {
	if explicitAlertCancel("上海市发布大风黄色预警", "上海市气象台发布预警，建议取消大型活动，及时关注预警解除通知。") {
		t.Fatal("defence paragraph became a cancellation")
	}
	if !explicitAlertCancel("上海市解除大风黄色预警", "上海市气象台发布解除信息。") {
		t.Fatal("explicit cancellation lost")
	}
}
func TestAlertThreeSourcesActualParsersMergeOnePublication(t *testing.T) {
	hs, s, f := newAlertFixture(t)
	a := fixtureOfficialAlert(t, f.now.Add(-time.Minute), "黄色")
	f.sh = []map[string]string{f.shAlert(a)}
	f.productAlert(a)
	f.nmc = []map[string]string{{"alertid": a.Identifier, "issuetime": a.PublishedAt.Format("2006/01/02 15:04"), "title": "上海市浦东新区气象台发布大风黄色预警信号", "url": "/publish/alarm/" + a.Identifier + ".html"}}
	f.nmcBodies[a.Identifier] = a.Body
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	events := alertEventsTest(t, s)
	if len(events) != 1 {
		t.Fatal("three sources replayed one publication", events)
	}
	r, err := alertRead[AlertRecord](t.Context(), s.repo(), "alert_record", a.Key)
	if err != nil || r.Alert.ExpiresAt.IsZero() {
		t.Fatal("structured detail expiry was not merged", r, err)
	}
	status, _ := s.Status(t.Context())
	if status.(map[string]any)["coverage"] != "healthy" {
		t.Fatal("one of the actual source parsers failed", status)
	}
	sent := 0
	e := alertEngine(hs, s, func() { sent++ })
	if err := e.Tick(t.Context(), f.now); err != nil || sent != 1 {
		t.Fatal("merged notification failed", sent, err)
	}
}
func TestAlertMalformedSourcesCannotVetoValidUpgrade(t *testing.T) {
	hs, s, f := newAlertFixture(t)
	a := fixtureOfficialAlert(t, f.now.Add(-time.Minute), "红色")
	f.sh = []map[string]string{f.shAlert(a)}
	f.nmc = []map[string]string{{"alertid": a.Identifier, "issuetime": a.PublishedAt.Format("2006/01/02 15:04"), "title": "上海市浦东新区红色大风预警", "url": "https://attacker.test/"}}
	f.national = [][]string{{"malformed"}}
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	events := alertEventsTest(t, s)
	if len(events) != 1 || events[0].Alert.Rank != 4 {
		t.Fatal("bad secondary sources suppressed a valid warning", events)
	}
	status, _ := s.Status(t.Context())
	if status.(map[string]any)["coverage"] != "degraded" {
		t.Fatal("malformed data was reported as healthy", status)
	}
}
func TestAlertParsingEmptyAndErrorAreDifferent(t *testing.T) {
	var rows []map[string]string
	for _, raw := range []string{`<html>WEB 应用防火墙</html>`, `alarm_callback(null)`, `alarm_callback([]); run_untrusted()`} {
		err := alertJSON([]byte(raw), "alarm_callback(", ")", &rows)
		if err == nil && rows != nil {
			t.Fatal("invalid wrapper/data accepted", raw)
		}
	}
	if a, err := canonicalAlert(OfficialAlert{Identifier: "x", Area: "浦东新区", Hazard: "大风", Level: "黄色", PublishedAt: timeMust("2026-10-27T10:00:00+08:00"), Body: "浦东新区气象台2026年10月27日09时59分发布大风黄色预警信号。"}, "", timeMust("2026-10-27T10:00:00+08:00")); err != nil || a.PublishedAt.Minute() != 59 {
		t.Fatal("issuer time failed canonical parsing", a, err)
	}
	if alertArea("上海市中心城区暴雨黄色预警", "") != "中心城区" || alertArea("上海市暴雨黄色预警", "") != "全市" {
		t.Fatal("central-city and municipality conflated")
	}
}

func TestAlertCancellationTombstoneRejectsLaggingSameMinutePositive(t *testing.T) {
	hs, s, f := newAlertFixture(t)
	a := fixtureOfficialAlert(t, f.now, "黄色")
	if err := s.ingest(t.Context(), a, hs.Options, 1); err != nil {
		t.Fatal(err)
	}
	cancel := a
	cancel.Cancel = true
	cancel.Body = "浦东新区气象台解除大风黄色预警。"
	if err := s.ingest(t.Context(), cancel, hs.Options, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.ingest(t.Context(), a, hs.Options, 1); err != nil {
		t.Fatal(err)
	}
	r, err := alertRead[AlertRecord](t.Context(), s.repo(), "alert_record", a.Key)
	if err != nil || !r.Alert.Cancel {
		t.Fatal("lagging source resurrected cancelled warning", r, err)
	}
	if explicitAlertCancel("上海市发布大风黄色预警", "上海市气象台发布：预计将有大风，未来将解除大风黄色预警。") {
		t.Fatal("future cancellation guidance became an explicit cancellation")
	}
	if !explicitAlertCancel("", "浦东新区气象台2026年10月27日10时00分解除大风黄色预警信号。") {
		t.Fatal("issuer's explicit cancellation was lost")
	}
}

func TestAlertMidSlotRestartDoesNotSkipNextFiveMinuteOccurrence(t *testing.T) {
	hs, s, f := newAlertFixture(t)
	f.now = f.now.Add(2*time.Minute + 20*time.Second)
	if err := s.Poll(t.Context(), hs.Options); err != nil {
		t.Fatal(err)
	}
	h, err := alertRead[AlertHealth](t.Context(), s.repo(), "alert_health", "nmc")
	want := timeMust("2026-10-27T10:05:00+08:00")
	if err != nil || !h.NextAttempt.Equal(want) {
		t.Fatal("mid-slot restart skipped the next scheduled check", h, err)
	}
	// Reopen the pre-fix state too: its success-based deadline was 10:07.
	if err := s.repo().transact(t.Context(), func(tx *sql.Tx) error {
		h.NextAttempt = timeMust("2026-10-27T10:07:00+08:00")
		return saveEntity(t.Context(), tx, "alert_health", "nmc", h)
	}); err != nil {
		t.Fatal(err)
	}
	requests := f.requests
	f.now = want
	if err := s.Poll(t.Context(), hs.Options); err != nil || f.requests <= requests {
		t.Fatal("next scheduled slot did not fetch", f.requests, requests, err)
	}
	hb, err := hs.Config(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	tr := hb.Triggers["weather_alerts"]
	tr.IntervalSeconds = 360
	hb.Triggers[tr.ID] = tr
	w := hb.Weekly["tuesday"]
	w.Start = "08:01"
	hb.Weekly["tuesday"] = w
	if got := nextAlertSlot(hb, timeMust("2026-10-27T08:04:00+08:00"), 360); !got.Equal(timeMust("2026-10-27T08:07:00+08:00")) {
		t.Fatal("cadence alignment ignored custom routine start", got)
	}
}
