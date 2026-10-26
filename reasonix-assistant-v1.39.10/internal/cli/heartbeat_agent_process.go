package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"reasonix/internal/heartbeat"
	"reasonix/internal/proc"
	"reasonix/internal/secrets"
)

type heartbeatAgentResult struct {
	Type          string `json:"type"`
	IsError       bool   `json:"is_error"`
	Result        string `json:"result"`
	SessionID     string `json:"session_id"`
	FromReasoning bool   `json:"result_from_reasoning"`
}

func (r heartbeatProcessRunner) runAgent(ctx context.Context, t heartbeat.Task) (heartbeatAgentResult, error) {
	var result heartbeatAgentResult
	cmd := proc.CommandContext(ctx, r.Executable, heartbeatAgentArgs(t, r.DefaultModel)...)
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = heartbeatAgentEnv(t)
	stdout, stderr := &heartbeatOutput{}, &heartbeatOutput{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if _, err := proc.RunCommand(ctx, cmd, proc.RunOptions{Source: "heartbeat"}); err != nil {
		return result, fmt.Errorf("Agent execution: %w: %s", err, secrets.Redact(string(stderr.data)))
	}
	if stdout.overflow {
		return result, errors.New("Agent result exceeds 1 MiB")
	}
	if err := json.Unmarshal(stdout.data, &result); err != nil {
		return result, fmt.Errorf("invalid Agent JSON result: %w", err)
	}
	if result.Type != "result" || result.IsError || result.FromReasoning {
		return result, errors.New("Agent did not produce a successful final answer")
	}
	return result, nil
}
