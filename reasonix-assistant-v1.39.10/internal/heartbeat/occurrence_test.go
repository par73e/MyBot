package heartbeat

import (
	"testing"
	"time"
)

func TestOpenObligationBelongsToReminderDayEvenAfterDelivery(t *testing.T) {
	day, _ := time.Parse(time.RFC3339, "2026-10-27T08:00:00+08:00")
	for _, tt := range []struct {
		task Task
		want bool
	}{{Task{At: "2026-10-27T15:00:00+08:00", Timezone: "Asia/Shanghai"}, true}, {Task{At: "2026-10-28T15:00:00+08:00", Timezone: "Asia/Shanghai"}, false}, {Task{Interval: "0 17 * * 2", Timezone: "Asia/Shanghai"}, true}, {Task{Interval: "0 17 * * 1", Timezone: "Asia/Shanghai"}, false}, {Task{Interval: "24h|daily@17:00", Timezone: "Asia/Shanghai"}, true}, {Task{At: "2026-10-26T15:00:00+08:00", SnoozedUntil: "2026-10-28T15:00:00+08:00", Timezone: "Asia/Shanghai"}, false}} {
		if got := tt.task.ScheduledOn(day); got != tt.want {
			t.Fatal(tt, got)
		}
	}
}
