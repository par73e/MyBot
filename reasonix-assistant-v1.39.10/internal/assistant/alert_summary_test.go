package assistant

import (
	"database/sql"
	"reasonix/internal/heartbeat"
	"strings"
	"testing"
)

func TestAlertSummaryKeepsCoreFactsFromUserExample(t *testing.T) {
	body := "浦东新区气象台2026年11月04日10时14分发布大风蓝色预警[IV级/一般]：预计未来24小时内本区大部街镇将出现阵风6-7级，沿江沿海地区7-8级的大风，请注意防范大风对高空作业、交通出行、设施农业等的不利影响。防御指南：1.政府及相关部门按照职责做好防大风工作；2.关好门窗，加固围板、棚架、广告牌。"
	e := AlertEvent{Kind: "new", Alert: OfficialAlert{Area: "浦东新区", Hazard: "大风", Level: "蓝色", PublishedAt: timeMust("2026-11-04T10:14:00+08:00"), Body: body, Source: "nmc", URL: "https://www.nmc.cn/publish/alarm/example.html"}}
	got := renderAlert(e)
	for _, fact := range []string{"浦东新区大风蓝色预警", "新发布", "11月04日 10:14", "未来24小时", "6-7级", "沿江沿海地区7-8级"} {
		if !strings.Contains(got, fact) {
			t.Errorf("missing %q: %s", fact, got)
		}
	}
	for _, noise := range []string{"气象台2026", "IV级", "防御指南", "政府", "设施农业", "https://", "来源："} {
		if strings.Contains(got, noise) {
			t.Errorf("contains %q: %s", noise, got)
		}
	}
	if len([]rune(got)) > 200 {
		t.Fatal("notification still too long", got)
	}
	if e.Alert.Body != body || e.Alert.URL == "" {
		t.Fatal("official record was modified")
	}
	t.Log(got)
}
func TestAlertSummariesCoverHazardsAndGuideVariations(t *testing.T) {
	for _, tt := range []struct{ hazard, body, fact string }{
		{"暴雨", "上海市气象台发布暴雨橙色预警信号：未来3小时累计降水量将达50毫米以上，部分道路可能积水。防御指南 1.有关单位开展排水。", "50毫米以上"},
		{"雷电", "预计未来6小时有雷电活动，局地伴有短时强降水。防范建议：部门做好防雷工作。", "未来6小时"},
		{"大雾", "能见度将降至200米以下，局地不足50米。（预警信息来源：国家预警信息发布中心）", "局地不足50米"},
		{"高温", "未来24小时最高气温将达到37℃以上。防护指南：安排停工。", "37℃以上"},
		{"道路结冰", "未来12小时部分道路可能出现影响交通的结冰。应对措施：交通部门启动预案。", "影响交通的结冰"},
		{"台风", "明天白天沿海阵风可达10—12级，今晚20时至明天08时雨势较强。防御指引：船舶回港。", "10—12级"},
	} {
		t.Run(tt.hazard, func(t *testing.T) {
			got := alertCoreSummary(tt.body)
			if !strings.Contains(got, tt.fact) {
				t.Fatal(got)
			}
			for _, noise := range []string{"指南", "建议：", "指引", "部门", "国家预警", "船舶"} {
				if strings.Contains(got, noise) {
					t.Fatal(got)
				}
			}
		})
	}
}
func TestAlertSummaryStatusMissingBodyAndLength(t *testing.T) {
	a := OfficialAlert{Area: "全市", Hazard: "暴雨", Level: "红色", PublishedAt: timeMust("2026-11-04T10:14:00+08:00"), ExpiresAt: timeMust("2026-11-04T16:14:00+08:00"), Body: "未来3小时降水量将达100毫米以上。"}
	for _, kind := range []string{"new", "upgrade", "update", "cancel"} {
		got := renderAlert(AlertEvent{Kind: kind, Alert: a})
		if kind == "cancel" {
			if !strings.Contains(got, "已解除") || strings.Contains(got, "100毫米") || strings.Contains(got, "有效至") {
				t.Fatal(got)
			}
		} else if !strings.Contains(got, "100毫米以上") || !strings.Contains(got, "有效至") {
			t.Fatal(got)
		}
	}
	a.Body = "防御指南：各部门做好防范工作。"
	got := renderAlert(AlertEvent{Kind: "new", Alert: a})
	if !strings.Contains(got, "暂未取得") || strings.Contains(got, "做好防范") {
		t.Fatal(got)
	}
	a.Cancel = true
	got = renderAlert(AlertEvent{Kind: "update", Alert: a})
	if !strings.Contains(got, "已解除") || strings.Contains(got, "安全") {
		t.Fatal(got)
	}
	long := "未来24小时阵风6-7级，" + strings.Repeat("部分地区可能受影响，", 40) + "防御指南：政府做好工作。"
	got = alertCoreSummary(long)
	if len([]rune(got)) > 181 || !strings.Contains(got, "6-7级") || !strings.HasSuffix(got, "…") {
		t.Fatal(got)
	}
}
func TestAlertPollingQueuesConciseNativeNotificationWithoutChangingSource(t *testing.T) {
	hs, s, f := newAlertFixture(t)
	a := fixtureOfficialAlert(t, f.now, "蓝色")
	a.Body = "未来24小时阵风6-7级，沿海7-8级。防御指南：政府做好工作。"
	f.sh = []map[string]string{f.shAlert(a)}
	if e := s.Poll(t.Context(), hs.Options); e != nil {
		t.Fatal(e)
	}
	jobs, e := (JobStore{Repository: s.repo()}).List(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, j := range jobs {
		if j.ModuleID == "weather_alert_notification" {
			found = true
			if j.Kind != "message" || !strings.Contains(j.Prompt, "7-8级") || strings.Contains(j.Prompt, "政府") {
				t.Fatal(j.Prompt)
			}
		}
	}
	if !found {
		t.Fatal("notification not queued")
	}
	events := alertEventsTest(t, s)
	if len(events) != 1 || events[0].Alert.Body != a.Body {
		t.Fatal("original body was not preserved", events)
	}
}

func TestAlertQueuedTextRefreshPreservesClaimedAndDeliveredJobs(t *testing.T) {
	for _, state := range []string{"queued", "claimed", "delivered"} {
		t.Run(state, func(t *testing.T) {
			hs, s, f := newAlertFixture(t)
			a := fixtureOfficialAlert(t, f.now, "蓝色")
			a.Body = "未来24小时阵风6-7级。防御指南：政府做好工作。"
			if e := s.ingest(t.Context(), a, hs.Options, 1); e != nil {
				t.Fatal(e)
			}
			event := alertEventsTest(t, s)[0]
			err := s.repo().transact(t.Context(), func(tx *sql.Tx) error {
				var job heartbeat.Task
				if e := alertLoad(t.Context(), tx, "job", event.JobID, &job); e != nil {
					return e
				}
				job.Prompt = "legacy full page defence guide"
				if state != "queued" {
					job.LastRunAt = f.now.UnixMilli()
					job.History = []heartbeat.Run{{Status: state}}
				}
				if e := saveEntity(t.Context(), tx, "job", job.ID, job); e != nil {
					return e
				}
				return s.queueAlert(t.Context(), tx, event, hs.Options)
			})
			if err != nil {
				t.Fatal(err)
			}
			job, e := alertRead[heartbeat.Task](t.Context(), s.repo(), "job", event.JobID)
			if e != nil {
				t.Fatal(e)
			}
			if (state == "queued") != (job.Prompt == renderAlert(event)) {
				t.Fatal("incorrect refresh", state, job.Prompt)
			}
			if job.ID != event.JobID || job.AlertEventID != event.ID || state != "queued" && len(job.History) != 1 {
				t.Fatal("delivery identity/history changed")
			}
		})
	}
}
