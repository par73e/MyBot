package assistant

import (
	"context"
	"strings"
	"testing"
)

func TestMorningReusesRankedNewsReadsOnlyThreeAndKeepsReferences(t *testing.T) {
	ns, _, snap := hotFixture(t)
	s := MorningService{Home: ns.Home, News: &ns, Clock: ns.Clock}
	page := s.prepareHotNews(t.Context())
	if len(page.Items) != 3 || page.SnapshotID != snap.ID || page.Ranking != snap.Ranking {
		t.Fatal(page)
	}
	for i, a := range page.Items {
		if a.ID != snap.Candidates[i].ID || a.Text == "" || a.URL != snap.Candidates[i].URL {
			t.Fatal("lost ranked facts", a)
		}
	}
	stored, err := ns.loadSnapshot(t.Context(), snap.ID)
	if err != nil || stored.Candidates[0].ReadAt.IsZero() || !stored.Candidates[3].ReadAt.IsZero() {
		t.Fatal("unexpected read set", err)
	}
}
func TestMorningNewsSkipsUnreadableCandidatesWithoutInventingFacts(t *testing.T) {
	ns, _, snap := hotFixture(t)
	for i := range 3 {
		snap.Candidates[i].Text = ""
		snap.Candidates[i].ContentStatus = "headline_only"
	}
	if err := ns.storeSnapshot(t.Context(), snap); err != nil {
		t.Fatal(err)
	}
	ns.Fetch = func(context.Context, string) ([]byte, error) { return []byte("<html></html>"), nil }
	page := (MorningService{Home: ns.Home, News: &ns, Clock: ns.Clock}).prepareHotNews(t.Context())
	if len(page.Items) != 3 || page.Items[0].ID != snap.Candidates[3].ID {
		t.Fatal(page)
	}
	out := MorningOutput{Weather: "暂未获取", News: []MorningNews{{ID: page.Items[0].ID, Summary: "模型A更新"}, {ID: page.Items[1].ID, Summary: "工具B开源"}, {ID: page.Items[2].ID, Summary: "芯片C发布"}}}
	text := renderMorning(MorningSnapshot{News: page, MailStatus: "ok", CourseStatus: "ok"}, out)
	lines := strings.Split(text, "\n")
	if len(lines) != 6 || !strings.Contains(lines[5], "模型A更新；工具B开源；芯片C发布") || strings.Contains(text, "来源") {
		t.Fatal(text)
	}
}
func TestNewsReportDoesNotAppendOperationalNotes(t *testing.T) {
	_, _, snap := hotFixture(t)
	snap.Status = "partial"
	out := NewsBriefingOutput{Events: []NewsEventSummary{{IDs: []string{snap.Candidates[0].ID}, Title: "模型更新", Summary: "官方称新增工具功能。"}}, Note: "补充说明：partial RSS超时，不是全网热榜。"}
	text := renderNewsBriefing(snap, out)
	if strings.Contains(text, "补充说明") || strings.Contains(text, "partial") || strings.Contains(text, "截至北京时间") || !strings.HasSuffix(text, "[原文]("+snap.Candidates[0].URL+")") {
		t.Fatal(text)
	}
}
