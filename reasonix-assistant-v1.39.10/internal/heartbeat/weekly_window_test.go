package heartbeat

import (
	"encoding/json"
	"testing"
	"time"
)

func requestedWeek() WeeklySchedule {
	starts := map[string]string{"monday": "10:00", "tuesday": "08:00", "wednesday": "10:00", "thursday": "10:00", "friday": "08:00", "saturday": "11:00", "sunday": "11:00"}
	week := WeeklySchedule{}
	for day, start := range starts {
		week[day] = DailyWindow{Enabled: true, Start: start, End: "24:00"}
	}
	return week
}

func TestWeeklyWindowAllDaysBoundariesTimezoneAndRepetition(t *testing.T) {
	_, task, _ := fixture(t)
	task.At, task.Interval = "", "60s"
	task.Kind = "mail_check"
	task.WeeklySchedule = requestedWeek()
	if err := task.Validate(); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	monday := time.Date(2026, 10, 26, 0, 0, 0, 0, loc)
	starts := []int{10, 8, 10, 10, 8, 11, 11}
	for week := range 2 {
		for day, hour := range starts {
			start := monday.AddDate(0, 0, week*7+day).Add(time.Duration(hour) * time.Hour)
			for _, tc := range []struct {
				at   time.Time
				want bool
			}{{start.Add(-time.Nanosecond), false}, {start, true}, {start.AddDate(0, 0, 0).Truncate(time.Second).Add(time.Minute), true}, {time.Date(start.Year(), start.Month(), start.Day()+1, 0, 0, 0, 0, loc), false}} {
				if task.WithinTimeWindow(tc.at.UTC()) != tc.want || task.Due(tc.at.UTC()) != tc.want {
					t.Fatalf("wrong admission at %s want %v", tc.at, tc.want)
				}
			}
		}
	}
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	var restored Task
	if err = json.Unmarshal(raw, &restored); err != nil || restored.WeeklySchedule["saturday"].Start != "11:00" {
		t.Fatal("weekly persistence", err)
	}
}

func TestWeeklyWindowOvernightAndDisabledDays(t *testing.T) {
	week := requestedWeek()
	for day := range week {
		week[day] = DailyWindow{}
	}
	week["sunday"] = DailyWindow{Enabled: true, Start: "22:00", End: "02:00"}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	for _, tc := range []struct {
		day, hour int
		want      bool
	}{{25, 21, false}, {25, 22, true}, {26, 0, true}, {26, 1, true}, {26, 2, false}, {26, 22, false}} {
		at := time.Date(2026, 10, tc.day, tc.hour, 0, 0, 0, loc)
		if week.Allows(at) != tc.want {
			t.Fatal(at, tc.want)
		}
	}
	if err := week.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWeeklyWindowValidation(t *testing.T) {
	for _, mutate := range []func(WeeklySchedule){
		func(w WeeklySchedule) { delete(w, "monday") },
		func(w WeeklySchedule) { w["Monday"] = w["monday"]; delete(w, "monday") },
		func(w WeeklySchedule) { w["monday"] = DailyWindow{Enabled: true, Start: "24:00", End: "10:00"} },
		func(w WeeklySchedule) { w["monday"] = DailyWindow{Enabled: true, Start: "10:00", End: "10:00"} },
		func(w WeeklySchedule) { w["monday"] = DailyWindow{Enabled: true, Start: "10:00", End: "24:01"} },
	} {
		week := requestedWeek()
		mutate(week)
		if err := week.Validate(); err == nil {
			t.Fatal("invalid week accepted", week)
		}
	}
	_, task, _ := fixture(t)
	task.WeeklySchedule = requestedWeek()
	task.TimeWindowStart = "08:00"
	if err := task.Validate(); err == nil {
		t.Fatal("mixed daily and weekly windows accepted")
	}
}
