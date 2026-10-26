package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/heartbeat"
)

type alertFixture struct {
	mu                    sync.Mutex
	now                   time.Time
	sh                    []map[string]string
	nmc                   []map[string]string
	national              [][]string
	details               map[string]map[string]string
	nmcBodies             map[string]string
	fail                  map[string]int
	nmcBlock              chan struct{}
	requests, notModified int
}

func newAlertFixture(t *testing.T) (HeartbeatService, AlertService, *alertFixture) {
	t.Helper()
	hs, _ := unifiedFixture(t)
	f := &alertFixture{now: timeMust("2026-10-27T10:00:00+08:00"), sh: []map[string]string{}, nmc: []map[string]string{}, national: [][]string{}, details: map[string]map[string]string{}, nmcBodies: map[string]string{}, fail: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	s := AlertService{Home: hs.Home, Clock: func() time.Time { return f.now }, Client: server.Client(), testOrigin: server.URL}
	yes := true
	if _, e := s.Apply(t.Context(), AlertRequest{Action: "configure", Enabled: &yes}); e != nil {
		t.Fatal(e)
	}
	return hs, s, f
}
func (f *alertFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests++
	now := f.now
	block := f.nmcBlock
	if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" {
		f.mu.Unlock()
		http.Error(w, "unexpected method or credentials", http.StatusBadRequest)
		return
	}
	w.Header().Set("Date", now.UTC().Format(http.TimeFormat))
	code := 0
	for path, v := range f.fail {
		if strings.Contains(r.URL.Path, path) {
			code = v
			break
		}
	}
	if code != 0 {
		f.mu.Unlock()
		if code == 429 {
			w.Header().Set("Retry-After", "1200")
		}
		http.Error(w, "untrusted upstream error", code)
		return
	}
	if strings.Contains(r.URL.Path, "findAlarm") && block != nil {
		f.mu.Unlock()
		select {
		case <-block:
		case <-r.Context().Done():
			return
		}
		f.mu.Lock()
	}
	var value any
	prefix, suffix := "", ""
	switch {
	case r.URL.Path == "/d1.weather.com.cn/shweather/alarm.json":
		value = f.sh
		prefix = "alarm_callback("
		suffix = ")"
	case strings.Contains(r.URL.Path, "findAlarm"):
		value = map[string]any{"code": 0, "data": map[string]any{"page": map[string]any{"pageNo": 1, "pageSize": 20, "count": len(f.nmc), "totalPage": 1, "list": f.nmc}}}
	case strings.HasPrefix(r.URL.Path, "/www.nmc.cn/publish/alarm/"):
		body := f.nmcBodies[strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/www.nmc.cn/publish/alarm/"), ".html")]
		f.mu.Unlock()
		_, _ = fmt.Fprint(w, `<div id="alarmtext">`+body+`</div>`)
		return
	case strings.HasSuffix(r.URL.Path, "grepalarm_cn.php"):
		value = map[string]any{"count": fmt.Sprint(len(f.national)), "data": f.national}
		prefix = "var alarminfo="
	case strings.Contains(r.URL.Path, "/alarm/webdata/"):
		value = f.details[strings.TrimPrefix(r.URL.Path, "/product.weather.com.cn/alarm/webdata/")]
		prefix = "var alarminfo="
	default:
		f.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	raw, _ := json.Marshal(value)
	body := prefix + string(raw) + suffix
	etag := `"` + mailHash(body) + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		f.notModified++
		f.mu.Unlock()
		w.WriteHeader(http.StatusNotModified)
		return
	}
	f.mu.Unlock()
	_, _ = fmt.Fprint(w, body)
}
func fixtureOfficialAlert(t *testing.T, at time.Time, level string) OfficialAlert {
	t.Helper()
	a, err := canonicalAlert(OfficialAlert{Identifier: "31011541600000_" + at.Format("20060102150405"), Area: "浦东新区", Hazard: "大风", Level: level, PublishedAt: at, ExpiresAt: at.Add(4 * time.Hour), Body: "浦东新区气象台发布大风" + level + "预警。", Source: "nmc", URL: alertNMC + "/f/alarm.html"}, "上海市浦东新区大风"+level+"预警", at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func (f *alertFixture) shAlert(a OfficialAlert) map[string]string {
	return map[string]string{"province": "上海市", "city": "上海市", "town": a.Area, "type": a.Hazard, "level": a.Level, "time": a.PublishedAt.Format("20060102150405"), "content": a.Body}
}
func (f *alertFixture) productAlert(a OfficialAlert) {
	file := "1010206-" + a.PublishedAt.Format("20060102150405") + "-0502.html"
	f.national = [][]string{{"上海市浦东新区", file, "121.5", "31.2", a.Identifier, a.Identifier, "上海市浦东新区发布大风" + a.Level + "预警信号"}}
	f.details[file] = map[string]string{"head": "上海市浦东新区发布大风" + a.Level + "预警信号", "PROVINCE": "上海市", "STATIONNAME": a.Area, "SIGNALTYPE": a.Hazard, "SIGNALLEVEL": a.Level, "ISSUETIME": a.PublishedAt.Format("2006-01-02 15:04:05"), "RELIEVETIME": a.ExpiresAt.Format("2006-01-02 15:04:05"), "ISSUECONTENT": a.Body, "identifier": a.Identifier, "references": a.Identifier, "msgType": "Alert"}
}
func alertEventsTest(t *testing.T, s AlertService) []AlertEvent {
	t.Helper()
	var events []AlertEvent
	if err := s.repo().transact(t.Context(), func(tx *sql.Tx) error {
		var e error
		events, e = loadEntities[AlertEvent](t.Context(), tx, "alert_event")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	return events
}
func alertEngine(h HeartbeatService, s AlertService, send func()) heartbeat.Engine {
	return heartbeat.Engine{Store: JobStore{Repository: s.repo()}, Clock: s.Clock, Gate: h.Allows, AfterDelivery: s.Acknowledge, Sender: courseSender(send), Runner: mailTestRunner(func(context.Context, heartbeat.Task) (heartbeat.Execution, error) {
		return heartbeat.Execution{}, fmt.Errorf("unexpected model invocation")
	})}
}
