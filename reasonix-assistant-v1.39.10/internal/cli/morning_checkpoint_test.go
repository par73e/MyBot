package cli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/assistant"
	"reasonix/internal/config"
	"reasonix/internal/heartbeat"
)

// Exercise the real subprocess error/output boundary with isolated native state.
func TestMorningCheckpointAgentHelper(t *testing.T) {
	mode := os.Getenv("MORNING_CHECKPOINT_HELPER")
	if mode == "" {
		return
	}
	resume := false
	for _, arg := range os.Args {
		resume = resume || arg == "--resume"
	}
	result := heartbeatAgentResult{Type: "result", IsError: true, Subtype: "error_during_execution", SessionID: "fixture-session", ErrorCode: "fixture_interruption", Result: "provider stream interrupted"}
	if mode == "recover" || resume {
		tool := assistant.MorningTool{Service: assistant.MorningService{Home: os.Getenv("MORNING_CHECKPOINT_FIXTURE_HOME")}}
		if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"snapshot"}`)); err != nil {
			panic(err)
		}
		draft := assistant.MorningOutput{Weather: "暂未获取", MailDecisions: []assistant.MailDecision{}, News: []assistant.MorningNews{{ID: "news1", Summary: "已核实的新模型消息"}}}
		args, _ := json.Marshal(map[string]any{"action": "check", "draft": draft})
		feedback, err := tool.Execute(context.Background(), args)
		if err != nil || !strings.Contains(feedback, `"valid":true`) {
			panic(feedback)
		}
		if mode == "retry-success" {
			raw, _ := json.Marshal(draft)
			result.IsError, result.Result, result.Subtype = false, string(raw), "success"
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
	if result.IsError {
		os.Exit(1)
	}
	os.Exit(0)
}

func writeCheckpointAgent(t *testing.T, runner heartbeatProcessRunner, mode string) {
	t.Helper()
	t.Setenv("MORNING_CHECKPOINT_HELPER", mode)
	t.Setenv("MORNING_CHECKPOINT_TEST_BINARY", os.Args[0])
	t.Setenv("MORNING_CHECKPOINT_FIXTURE_HOME", config.MemoryUserDir())
	writeMorningAgent(t, runner, `printf '%s\n' "$*" >> "$REASONIX_HOME/calls"
exec "$MORNING_CHECKPOINT_TEST_BINARY" -test.run='^TestMorningCheckpointAgentHelper$' -- "$@"
`)
}

func TestMorningRunnerRecoversCheckedDraftAfterFailedProcessExactlyOnce(t *testing.T) {
	runner, job, store := morningRunnerFixture(t, 5*time.Minute)
	writeCheckpointAgent(t, runner, "recover")
	sender := &morningTestSender{}
	service := assistant.MorningService{Home: config.MemoryUserDir()}
	newEngine := func() heartbeat.Engine {
		return heartbeat.Engine{Store: store, Runner: runner, Sender: sender, Gate: (assistant.HeartbeatService{Home: config.MemoryUserDir()}).Allows, AfterDelivery: service.Acknowledge}
	}
	engine := newEngine()
	if err := engine.Tick(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.List(t.Context())
	if err != nil || jobs[0].History[0].Status != "delivered" || !strings.Contains(jobs[0].History[0].Result, "已核实的新模型消息") || strings.Contains(jobs[0].History[0].DecisionReason, "降级") {
		t.Fatal(jobs, err)
	}
	engine = newEngine()
	if err := engine.Tick(t.Context(), time.Now()); err != nil || sender.calls != 1 {
		t.Fatal("recovered briefing replayed after restart", sender.calls, err)
	}
	calls, err := os.ReadFile(config.MemoryUserDir() + "/calls")
	if err != nil || strings.Count(string(calls), "--output-format") != 1 {
		t.Fatal("checked draft recovery called the model again", string(calls), err)
	}
	latest, err := (assistant.MorningTool{Service: service}).Execute(t.Context(), json.RawMessage(`{"action":"latest"}`))
	if err != nil || strings.Contains(latest, `"fallback":true`) || !strings.Contains(latest, job.ID) {
		t.Fatal(latest, err)
	}
}

func TestMorningRunnerResumesFailedExecutionAndRecoversRepairCheckpoint(t *testing.T) {
	for _, mode := range []string{"retry-success", "retry-interrupted"} {
		t.Run(mode, func(t *testing.T) {
			runner, job, store := morningRunnerFixture(t, 5*time.Minute)
			if err := store.Update(t.Context(), job.ID, func(v *heartbeat.Task) error {
				v.History = []heartbeat.Run{{StartedAt: job.RunStartedAt, Status: "running"}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			writeCheckpointAgent(t, runner, mode)
			out, err := runner.Run(t.Context(), job)
			if err != nil || !strings.Contains(out.Text, "已核实的新模型消息") || strings.Contains(out.DecisionReason, "降级") {
				t.Fatal(out, err)
			}
			calls, err := os.ReadFile(config.MemoryUserDir() + "/calls")
			if err != nil || strings.Count(string(calls), "--output-format") != 2 || !strings.Contains(string(calls), "--resume fixture-session") {
				t.Fatal("failed execution did not resume its original session once", string(calls), err)
			}
		})
	}
}
