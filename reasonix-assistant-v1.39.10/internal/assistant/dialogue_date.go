package assistant

import (
	"os"
	"time"
)

// Only human dialogue uses the sleep-day convention. Clocks, validity intervals,
// scheduled analyses and provider data always retain their civil timestamps.
const dialogueDateInstructions = `DATE INTERPRETATION FOR THIS HUMAN TURN: now and calendar_reference are the real civil clock/calendar. dialogue_date_reference supplies the conversational day, shared with the 06:00 Asia/Shanghai conversation boundary. Before 06:00, default unqualified 今天/昨天/明天/第二天/后天 in the user's own informal planning to dialogue_date_reference. During that window 明天/第二天 means the upcoming daytime on the current civil date. This is a language default, NOT evidence that the user is awake or asleep. Explicit YYYY-MM-DD dates, weekdays (e.g. 周五), civil-date wording (日历上的明天/过了零点算明天), or a clear statement that the user has already woken up and begun a new day take precedence; use calendar_reference for the latter civil-day reading. 起床后/睡醒后/今天上午 with a future clock time normally means the upcoming civil daytime, not the previous morning. Week expressions such as 下周 retain calendar_reference's civil Monday boundaries. Never shift a supplied absolute timestamp, weekday, countdown (两小时后), external document's relative dates, weather/course data, routine windows, or memory validity by six hours. Interpret a quoted/historical 第二天 relative to its stated event, not automatically to now. If these cues conflict or a computed reminder would be in the past, ask one focused question; never silently roll it forward a day. Persist the resulting absolute civil date/time in task/memory/location fields. After a successful reminder write, confirm its exact month/day, weekday and clock time, not only 明天, and report any routine-window skip warnings.`

type dialogueDateReference struct {
	Timezone         string `json:"timezone"`
	DayStartsAt      string `json:"day_starts_at"`
	CivilDate        string `json:"civil_date"`
	BeforeDayStart   bool   `json:"before_day_start"`
	Today            string `json:"today"`
	Yesterday        string `json:"yesterday"`
	Tomorrow         string `json:"tomorrow"`
	DayAfterTomorrow string `json:"day_after_tomorrow"`
}

func dialogueDates(now time.Time) *dialogueDateReference {
	if os.Getenv("REASONIX_ASSISTANT_BACKGROUND") == "1" {
		return nil
	}
	civil := now.In(mustShanghai())
	// Reuse the actual session boundary, including month/year rollover.
	day := DailyConversation("", now).StartsAt
	return &dialogueDateReference{
		Timezone: DailyTimezone, DayStartsAt: "06:00",
		CivilDate: civil.Format(time.DateOnly), BeforeDayStart: civil.Hour() < 6,
		Today: day.Format(time.DateOnly), Yesterday: day.AddDate(0, 0, -1).Format(time.DateOnly),
		Tomorrow: day.AddDate(0, 0, 1).Format(time.DateOnly), DayAfterTomorrow: day.AddDate(0, 0, 2).Format(time.DateOnly),
	}
}
