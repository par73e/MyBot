package assistant

import (
	"strings"
	"testing"
	"time"
)

func TestBriefingKeepsLongTodoAndDistinguishesReminderTime(t *testing.T) {
	title := "提交课程作业截止时间是明天上午十点不要遗漏附件"
	s := MorningSnapshot{Date: "2026-10-28", Todos: []Todo{{Title: title}}}
	text := renderMorning(s, MorningOutput{})
	if !strings.Contains(text, title) {
		t.Fatal(text)
	}
	evening := renderEvening(EveningSnapshot{Tasks: []Todo{{Title: title}}, TomorrowReminder: title, TomorrowReminderAt: timeMust("2026-10-29T08:00:00+08:00"), AsOf: time.Now()}, "晚安。")
	if !strings.Contains(evening, title) || !strings.Contains(evening, "08:00会提醒您") {
		t.Fatal(evening)
	}
}
func TestNewsWarningsOnlyWhenMostSourcesUnavailable(t *testing.T) {
	sources := []NewsSourceState{{Status: "ok"}, {Status: "unavailable"}, {Status: "unavailable"}}
	snap := HotNewsSnapshot{Sources: sources, Candidates: []HotNewsArticle{{ID: "x"}}}
	out := NewsBriefingOutput{Events: []NewsEventSummary{{IDs: []string{"x"}, Title: "标题", Summary: "摘要"}}}
	if !strings.Contains(renderNewsBriefing(snap, out), "来源较少") {
		t.Fatal("missing warning")
	}
	snap.Sources[1].Status = "ok"
	if strings.Contains(renderNewsBriefing(snap, out), "来源较少") {
		t.Fatal("unexpected warning")
	}
}
