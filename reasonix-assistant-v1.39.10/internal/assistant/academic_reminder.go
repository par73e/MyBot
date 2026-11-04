package assistant

import (
	"context"
	"errors"
	"reasonix/internal/heartbeat"
	"time"
)

const SchooldayReminderText = "Sir，记得用 UU 远程打开宿舍里的 Windows 电脑。"

func schooldayReminderText() string { return neutralInstanceInstructions(SchooldayReminderText) }

func academicReminderEligible(home string, at time.Time) bool {
	c, e := loadAcademic(home)
	if e != nil {
		return false
	}
	d := c.Day(at)
	return d.Known && d.Teaching
}
func (s HeartbeatService) academicReminderAdmission(ctx context.Context, t heartbeat.Task, now time.Time) (bool, error) {
	if !academicReminderEligible(s.Home, t.ScheduledTime(now)) {
		return false, heartbeat.ErrOccurrenceChanged
	}
	c, e := s.Config(ctx)
	if e != nil {
		return false, e
	}
	if !c.ModuleEnabled["schoolday_reminder"] {
		return false, heartbeat.ErrOccurrenceChanged
	}
	tr, ok := c.Triggers[t.TriggerID]
	if !ok || !tr.Enabled {
		return false, heartbeat.ErrOccurrenceChanged
	}
	if t.At == "" || t.Prompt != schooldayReminderText() {
		return false, errors.New("schoolday reminder does not match its native contract")
	}
	return s.allowsModule(ctx, c, t, now)
}
