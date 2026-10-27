package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"reasonix/internal/heartbeat"
	"reasonix/internal/proc"
	"reasonix/internal/secrets"
)

type heartbeatAgentResult struct {
	Type          string `json:"type"`
	Subtype       string `json:"subtype"`
	ErrorCode     string `json:"error_code"`
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
	_, runErr := proc.RunCommand(ctx, cmd, proc.RunOptions{Source: "heartbeat"})
	if stdout.overflow {
		return result, errors.Join(runErr, errors.New("Agent result exceeds 1 MiB"))
	}
	decodeErr := json.Unmarshal(stdout.data, &result)
	if runErr != nil {
		if decodeErr != nil {
			runErr = errors.Join(runErr, fmt.Errorf("invalid Agent JSON result: %w", decodeErr))
		}
		return result, heartbeatAgentFailure(runErr, result, string(stderr.data))
	}
	if decodeErr != nil {
		return result, fmt.Errorf("invalid Agent JSON result: %w", decodeErr)
	}
	if result.Type != "result" || result.IsError || result.FromReasoning {
		return result, heartbeatAgentFailure(errors.New("Agent did not produce a successful final answer"), result, string(stderr.data))
	}
	return result, nil
}

func heartbeatAgentFailure(err error, result heartbeatAgentResult, stderr string) error {
	detail := ""
	if result.IsError && !result.FromReasoning {
		detail = strings.TrimSpace(result.Result)
		// An interrupted structured answer is private draft data, not a useful
		// diagnostic. Keep its size rather than copying mail into the journal.
		if strings.HasPrefix(detail, "{") || strings.HasPrefix(detail, "[") {
			detail = fmt.Sprintf("interrupted structured result (%d bytes)", len(detail))
		}
	}
	detail = secrets.Redact(strings.TrimSpace(detail + "\n" + stderr))
	runes := []rune(detail)
	if len(runes) > 2048 {
		detail = string(runes[:2048]) + " [truncated]"
	}
	return fmt.Errorf("Agent execution: %w (subtype=%q code=%q session=%q): %s", err, secrets.Redact(result.Subtype), secrets.Redact(result.ErrorCode), secrets.Redact(result.SessionID), detail)
}
