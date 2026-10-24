package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"reasonix/internal/assistant"
	"reasonix/internal/config"
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
	if t.DecideNotification {
		cmd.Env = append(os.Environ(), "REASONIX_ASSISTANT_BACKGROUND=1", "REASONIX_ASSISTANT_JOB_ID="+t.ID)
	}
	if t.Capability == "mail" {
		cmd.Env = append(cmd.Env, "REASONIX_ASSISTANT_MAIL_BACKGROUND=1", fmt.Sprintf("REASONIX_ASSISTANT_MAIL_ATTEMPT=%d", t.RunStartedAt))
		if t.ModuleID == "mail_digest" {
			cmd.Env = append(cmd.Env, "REASONIX_ASSISTANT_MAIL_DIGEST=1")
		}
	}
	if t.Capability == "weather" {
		cmd.Env = append(cmd.Env, "REASONIX_ASSISTANT_WEATHER_BACKGROUND=1", fmt.Sprintf("REASONIX_ASSISTANT_WEATHER_ATTEMPT=%d", t.RunStartedAt))
	}
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
	if t.Capability == "mail" {
		if err := assistant.NewMailService(config.MemoryUserDir()).VerifyMailReads(ctx, t.ID, t.RunStartedAt, t.MailIDs); err != nil {
			return heartbeat.Execution{}, err
		}
	}
	if t.Capability == "weather" {
		if err := assistant.NewWeatherService(config.MemoryUserDir()).VerifyWeatherRead(ctx, t.ID, t.RunStartedAt); err != nil {
			return heartbeat.Execution{}, err
		}
		var decision struct {
			Notify  bool   `json:"notify"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(result.Result), &decision) != nil || !decision.Notify || strings.TrimSpace(decision.Message) == "" {
			return heartbeat.Execution{}, errors.New("morning task requires a nonempty daily notification")
		}
	}
	text := strings.TrimSpace(result.Result)
	if text == "HEARTBEAT_OK" {
		text = ""
	}
	reason := ""
	if t.DecideNotification {
		var envelope struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal([]byte(text), &envelope)
		reason = envelope.Reason
		var err error
		text, err = heartbeat.NotificationText(text)
		if err != nil {
			return heartbeat.Execution{}, err
		}
	}
	if t.Capability == "mail" {
		service := assistant.NewMailService(config.MemoryUserDir())
		var err error
		if t.ModuleID == "mail_digest" {
			err = service.VerifyDigest(ctx, t, result.Result)
		} else if len(t.MailIDs) > 0 {
			err = service.RecordMailDecisions(ctx, t, result.Result)
		}
		if err != nil {
			return heartbeat.Execution{}, err
		}
	}
	return heartbeat.Execution{Text: text, SessionID: result.SessionID, DecisionReason: reason}, nil
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
	if t.Capability == "mail" || t.Capability == "weather" {
		steps := "24"
		if t.ModuleID == "mail_digest" {
			steps = "64"
		}
		args = append(args, "--max-steps", steps)
	}
	prompt := t.Prompt
	if t.DecideNotification {
		switch {
		case t.ModuleID == "mail_digest":
			prompt += assistant.MailDigestContract
		case t.Capability == "mail" && len(t.MailIDs) > 0:
			prompt += assistant.MailDecisionContract
		default:
			prompt += heartbeat.DecisionInstructions
		}
	}
	return append(args, "--", prompt)
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
