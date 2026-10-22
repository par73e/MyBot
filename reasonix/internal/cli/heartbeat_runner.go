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

type heartbeatProcessRunner struct {
	Executable   string
	DefaultModel string
}

func (r heartbeatProcessRunner) Run(ctx context.Context, t heartbeat.Task) (heartbeat.Execution, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	args := heartbeatAgentArgs(t, r.DefaultModel)
	cmd := proc.CommandContext(ctx, r.Executable, args...)
	cmd.WaitDelay = 5 * time.Second
	stdout, stderr := &heartbeatOutput{}, &heartbeatOutput{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	_, err := proc.RunCommand(ctx, cmd, proc.RunOptions{Source: "heartbeat"})
	if err != nil {
		return heartbeat.Execution{}, fmt.Errorf("Agent execution: %w: %s", err, secrets.Redact(string(stderr.data)))
	}
	if stdout.overflow {
		return heartbeat.Execution{}, errors.New("Agent result exceeds 1 MiB")
	}
	var result struct {
		Type          string `json:"type"`
		IsError       bool   `json:"is_error"`
		Result        string `json:"result"`
		SessionID     string `json:"session_id"`
		FromReasoning bool   `json:"result_from_reasoning"`
	}
	if err := json.Unmarshal(stdout.data, &result); err != nil {
		return heartbeat.Execution{}, fmt.Errorf("invalid Agent JSON result: %w", err)
	}
	if result.Type != "result" || result.IsError || result.FromReasoning {
		return heartbeat.Execution{}, errors.New("Agent did not produce a successful final answer")
	}
	text := strings.TrimSpace(result.Result)
	if text == "HEARTBEAT_OK" {
		text = ""
	}
	return heartbeat.Execution{Text: text, SessionID: result.SessionID}, nil
}

func heartbeatAgentArgs(t heartbeat.Task, defaultModel string) []string {
	args := []string{"run", "--output-format", "json", "--permission-mode", t.ApprovalMode, "--dir", t.WorkspaceRoot}
	model := t.Model
	if model == "" {
		model = defaultModel
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	if !t.NewConversationEachRun && len(t.History) > 0 {
		if session := t.History[len(t.History)-1].SessionID; session != "" {
			args = append(args, "--resume", session)
		}
	}
	return append(args, "--", t.Prompt)
}

type heartbeatOutput struct {
	data     []byte
	overflow bool
}

func (b *heartbeatOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (1 << 20) - len(b.data)
	if len(p) > remaining {
		p = p[:remaining]
		b.overflow = true
	}
	b.data = append(b.data, p...)
	return n, nil
}
