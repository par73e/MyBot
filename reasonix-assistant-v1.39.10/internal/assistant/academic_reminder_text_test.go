package assistant

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/heartbeat"
)

type academicTextSender struct{ messages []string }

func (s *academicTextSender) Send(_ context.Context, _ heartbeat.Target, text string) error {
	s.messages = append(s.messages, text)
	return nil
}

func TestAcademicReminderSendsPersonalAddressAndNaturalTextOnce(t *testing.T) {
	for _, address := range []string{"Sir", "小林", ""} {
		t.Run("address="+address, func(t *testing.T) {
			s, _ := schoolReminderFixture(t)
			t.Setenv("ASROBOT_INSTANCE_MODE", "isolated")
			t.Setenv("REASONIX_HOME", s.Home)
			t.Setenv("REASONIX_ASSISTANT_ADDRESS", "")
			if err := os.Unsetenv("REASONIX_ASSISTANT_ADDRESS"); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]string{"name": "助手", "address": address})
			if err := os.WriteFile(filepath.Join(s.Home, "assistant", "profile.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			now := timeMust("2026-11-06T10:55:00+08:00")
			sender := &academicTextSender{}
			engine := heartbeat.Engine{
				Store: JobStore{Repository: s.repo()}, Prepare: s.Prepare, Gate: s.Allows,
				Sender: sender, Clock: func() time.Time { return now },
				Runner: mailTestRunner(func(context.Context, heartbeat.Task) (heartbeat.Execution, error) {
					t.Fatal("UU reminder invoked model")
					return heartbeat.Execution{}, nil
				}),
			}
			if err := engine.Tick(t.Context(), now); err != nil {
				t.Fatal(err)
			}
			// A fresh service/store must honor the persisted occurrence after restart.
			restarted := HeartbeatService{Home: s.Home, Options: s.Options}
			engine.Prepare, engine.Gate = restarted.Prepare, restarted.Allows
			engine.Store = JobStore{Repository: restarted.repo()}
			if err := engine.Tick(t.Context(), now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			want := "到时间了，记得用 UU 远程打开宿舍里的电脑。"
			if address != "" {
				want = address + "，" + want
			}
			if len(sender.messages) != 1 || sender.messages[0] != want {
				t.Fatalf("delivered messages = %q, want once: %q", sender.messages, want)
			}
		})
	}
}
