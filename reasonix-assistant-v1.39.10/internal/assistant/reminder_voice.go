package assistant

import (
	"errors"
	"reasonix/internal/heartbeat"
	"strings"
)

func validateReminderVoice(body string, n *heartbeat.ReminderNotification) error {
	if n != nil && n.Mode == "verbatim" {
		return nil
	}
	for _, prefix := range []string{"Sir 要求", "Sir要求", "根据用户指示", "用户要求", "用户让我", "Sir 让我", "Sir让我"} {
		if strings.HasPrefix(strings.TrimSpace(body), prefix) {
			return errors.New("reminder body must speak directly to Sir at delivery; rewrite the request-report wording, or use verbatim only for an explicit exact-text request")
		}
	}
	return nil
}
func sameNotification(a, b *heartbeat.ReminderNotification) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
