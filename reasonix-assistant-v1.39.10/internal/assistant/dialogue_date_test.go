package assistant

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDialogueDatesAcrossSleepDayBoundary(t *testing.T) {
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "")
	for _, tc := range []struct {
		now, today, tomorrow, after string
		late                        bool
	}{
		{"2026-11-04T23:40:00+08:00", "2026-11-04", "2026-11-05", "2026-11-06", false},
		{"2026-11-05T00:00:00+08:00", "2026-11-04", "2026-11-05", "2026-11-06", true},
		{"2026-11-05T00:20:00+08:00", "2026-11-04", "2026-11-05", "2026-11-06", true},
		{"2026-11-05T05:59:59+08:00", "2026-11-04", "2026-11-05", "2026-11-06", true},
		{"2026-11-05T06:00:00+08:00", "2026-11-05", "2026-11-06", "2026-11-07", false},
		{"2026-11-08T16:20:00Z", "2026-11-08", "2026-11-09", "2026-11-10", true},
		{"2026-12-31T16:20:00Z", "2026-12-31", "2027-01-01", "2027-01-02", true},
		{"2028-03-01T00:20:00+08:00", "2028-02-29", "2028-03-01", "2028-03-02", true},
	} {
		t.Run(tc.now, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tc.now)
			if err != nil {
				t.Fatal(err)
			}
			got := dialogueDates(now)
			if got.Today != tc.today || got.Tomorrow != tc.tomorrow || got.DayAfterTomorrow != tc.after || got.BeforeDayStart != tc.late {
				t.Fatalf("incorrect conversational dates: %+v", got)
			}
			if got.Today != DailyConversation("owner", now).Date {
				t.Fatal("dialogue and session dates disagree")
			}
		})
	}
}

func TestLateNightContextKeepsCivilCalendarAndBackgroundSemantics(t *testing.T) {
	home := t.TempDir()
	now := timeMust("2026-11-09T00:20:00+08:00") // Monday, still Sunday's conversation.
	for _, background := range []string{"", "1"} {
		t.Setenv("REASONIX_ASSISTANT_BACKGROUND", background)
		raw, err := ContextFor(t.Context(), home, now)
		if err != nil {
			t.Fatal(err)
		}
		start, end := strings.Index(raw, "\n{"), strings.LastIndex(raw, "\n</personal_assistant_current_state>")
		var state struct {
			Now      string                 `json:"now"`
			Calendar map[string]string      `json:"calendar_reference"`
			Dialogue *dialogueDateReference `json:"dialogue_date_reference"`
		}
		if start < 0 || end < start {
			t.Fatal("missing state JSON")
		}
		if err := json.Unmarshal([]byte(raw[start:end]), &state); err != nil {
			t.Fatal(err)
		}
		if state.Now != now.Format(time.RFC3339) || state.Calendar["today"] != "2026-11-09" || state.Calendar["tomorrow"] != "2026-11-10" || state.Calendar["next_week_monday"] != "2026-11-16" {
			t.Fatal("civil clock/week boundaries were shifted", state)
		}
		if background == "1" {
			if state.Dialogue != nil || strings.Contains(raw, dialogueDateInstructions) {
				t.Fatal("sleep-day policy leaked into scheduled analyses")
			}
		} else if state.Dialogue == nil || state.Dialogue.Tomorrow != "2026-11-09" || !strings.Contains(raw, dialogueDateInstructions) {
			t.Fatal("human turn did not receive resolved dates and policy")
		}
	}
}

func TestLateNightTaskPersistsResolvedCivilTimestamp(t *testing.T) {
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "")
	s, _ := fixture(t)
	s.Now = func() time.Time { return timeMust("2026-11-05T00:20:00+08:00") }
	for _, at := range []string{
		"2026-11-05T13:00:00+08:00", // 明天, resolved from the sleep day.
		"2026-11-06T13:00:00+08:00", // Explicit 周五 must stay Friday.
	} {
		v := createTask(t, s, TaskRequest{Title: "交报告 " + at, Kind: "reminder", Body: "记得交报告", At: at})
		if v.Schedule == nil || v.Schedule.At != at {
			t.Fatal("resolved timestamp was shifted during persistence", v)
		}
	}
}
