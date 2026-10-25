package heartbeat

import (
	"testing"
	"time"
)

func TestNaturalReminderActualClockAndLiteral(t *testing.T) {
	j := Task{Prompt: "Sir，记得吃晚饭。", Timezone: "Asia/Shanghai", Notification: &ReminderNotification{Mode: "time_aware", ActionText: "记得吃饭"}}
	for _, tt := range []struct{ at, want string }{{"2026-10-25T18:00:00+08:00", "Sir，六点了，记得吃饭。"}, {"2026-10-25T18:20:00+08:00", "Sir，六点二十分了，记得吃饭。"}} {
		now, _ := time.Parse(time.RFC3339, tt.at)
		if got := j.MessageText(now); got != tt.want {
			t.Fatal(got, tt.want)
		}
	}
	j.Notification.Mode = "verbatim"
	if j.MessageText(time.Now()) != j.Prompt {
		t.Fatal("literal changed")
	}
}
func TestReminderVoiceDoesNotNeedModel(t *testing.T) {
	store, task, now := fixture(t)
	if err := store.Update(t.Context(), task.ID, func(j *Task) error {
		j.Kind = "message"
		j.Prompt = "Sir，记得吃饭。"
		j.Notification = &ReminderNotification{Mode: "time_aware", ActionText: "记得吃饭"}
		j.Target = &Target{ConnectionID: "weixin", ChatID: "sir"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runner := &testRunner{}
	sender := &testSender{}
	engine := Engine{Store: store, Runner: runner, Sender: sender, Clock: func() time.Time { return now }}
	if err := engine.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 0 || sender.calls != 1 || sender.texts[0] != "Sir，五点了，记得吃饭。" {
		t.Fatal(runner.calls, sender.texts)
	}
	engine.Store = Store{Path: store.Path}
	if err := engine.Tick(t.Context(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if sender.calls != 1 {
		t.Fatal("restart duplicated reminder")
	}
}
