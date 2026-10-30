package assistant

import (
	"strings"
	"testing"
)

func TestMorningRelativeReminderTitle(t *testing.T) {
	todo := Todo{Title: "参加课程讨论（明天13:00）", JobID: "job", Revision: 1, CreatedAt: timeMust("2026-10-28T21:11:00+08:00")}
	tests := []struct{ date, at, want string }{
		{"2026-10-29", "2026-10-29T13:00:00+08:00", "参加课程讨论（13:00提醒）"},
		{"2026-10-29", "2026-10-29T12:00:00+08:00", todo.Title},
		{"2026-10-29", "2026-10-30T13:00:00+08:00", todo.Title},
		{"2026-10-29", "", todo.Title},
	}
	for _, tt := range tests {
		if got := morningReminderTitle(todo, tt.date, tt.at); got != tt.want {
			t.Errorf("got %q want %q", got, tt.want)
		}
	}
	s := MorningSnapshot{Date: "2026-10-29", Todos: []Todo{todo}, ReminderTimes: map[string]string{"job": "2026-10-29T13:00:00+08:00"}}
	if text := renderMorning(s, MorningOutput{}); !strings.Contains(text, "今天记得参加课程讨论（13:00提醒）") || strings.Contains(text, "明天") {
		t.Fatal(text)
	}
}
func TestMorningReminderWithStableTitle(t *testing.T) {
	todo := Todo{Title: "参加课程讨论", Kind: "reminder"}
	got := morningReminderTitle(todo, "2026-10-29", "2026-10-29T13:00:00+08:00")
	if got != "参加课程讨论（13:00提醒）" {
		t.Fatal(got)
	}
}

func TestMorningRelativeSuffixDoesNotStripOtherMeaning(t *testing.T) {
	for _, title := range []string{"讨论明天的课程（今天13:00开会）", "今天要完成论文", "（明天13:00）"} {
		todo := Todo{Title: title}
		if got := morningReminderTitle(todo, "2026-10-29", "2026-10-29T13:00:00+08:00"); got != title {
			t.Fatal(title, got)
		}
	}
}

func TestMorningFactsProvideScheduledTime(t *testing.T) {
	h, tasks, _ := expansionFixture(t)
	view := createTask(t, tasks, TaskRequest{Title: "参加课程讨论（明天13:00）", Kind: "reminder", Body: "提醒", At: "2026-10-26T13:00:00+08:00"})
	s := MorningService{Home: h.Home}
	snap := MorningSnapshot{ID: "relative-test", Date: "2026-10-26", CreatedAt: timeMust("2026-10-26T09:50:00+08:00")}
	if err := s.refreshFacts(t.Context(), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.ReminderTimes[view.JobID] != "2026-10-26T13:00:00+08:00" {
		t.Fatal(snap.ReminderTimes, view.JobID)
	}
	if text := renderMorning(snap, MorningOutput{}); !strings.Contains(text, "参加课程讨论（13:00提醒）") {
		t.Fatal(text)
	}
}
