package heartbeat

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// ReminderNotification is bounded native rendering, never a template program.
type ReminderNotification struct {
	Mode       string `json:"mode"`
	ActionText string `json:"action_text,omitempty"`
}

func validateReminderNotification(t Task) error {
	if t.Notification == nil {
		return nil
	}
	if t.Kind != "message" {
		return errors.New("notification formatting requires a fixed message reminder")
	}
	n := t.Notification
	if n.Mode != "direct" && n.Mode != "verbatim" && n.Mode != "time_aware" {
		return errors.New("unknown reminder notification mode")
	}
	if n.Mode == "time_aware" && (strings.TrimSpace(n.ActionText) == "" || utf8.RuneCountInString(n.ActionText) > 200 || strings.ContainsAny(n.ActionText, "\r\n")) {
		return errors.New("time-aware reminder requires a short action_text")
	}
	return nil
}

func reminderClock(now time.Time) string {
	numbers := []string{"零", "一", "二", "三", "四", "五", "六", "七", "八", "九", "十", "十一", "十二"}
	h := now.Hour() % 12
	if h == 0 {
		h = 12
	}
	clock := numbers[h] + "点"
	m := now.Minute()
	if m == 0 {
		return clock
	}
	if m < 10 {
		return clock + "零" + numbers[m] + "分"
	}
	if m < 20 {
		return clock + "十" + strings.TrimPrefix(numbers[m-10], "零") + "分"
	}
	return clock + numbers[m/10] + "十" + strings.TrimPrefix(numbers[m%10], "零") + "分"
}

func (t Task) MessageText(now time.Time) string {
	if t.Notification == nil || t.Notification.Mode != "time_aware" {
		return t.Prompt
	}
	z, err := time.LoadLocation(t.Timezone)
	if err != nil {
		return t.Prompt
	}
	return fmt.Sprintf("Sir，%s了，%s。", reminderClock(now.In(z)), strings.TrimRight(strings.TrimSpace(t.Notification.ActionText), "。.!！"))
}
