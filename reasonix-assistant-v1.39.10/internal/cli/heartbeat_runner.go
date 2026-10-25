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
	var prepareErr error
	t, prepareErr = prepareHeartbeatSources(ctx, t)
	if prepareErr != nil {
		return heartbeat.Execution{}, prepareErr
	}
	ctx, cancel := context.WithTimeout(ctx, heartbeatModelTimeout(t))
	defer cancel()
	args := heartbeatAgentArgs(t, r.DefaultModel)
	cmd := proc.CommandContext(ctx, r.Executable, args...)
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = heartbeatAgentEnv(t)
	stdout, stderr := &heartbeatOutput{}, &heartbeatOutput{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	_, err := proc.RunCommand(ctx, cmd, proc.RunOptions{Source: "heartbeat"})
	if err != nil {
		if t.ModuleID == "evening_briefing" {
			return eveningFallback(context.Background(), t)
		}
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
		if t.ModuleID == "evening_briefing" {
			return eveningFallback(context.Background(), t)
		}
		return heartbeat.Execution{}, fmt.Errorf("invalid Agent JSON result: %w", err)
	}
	if result.Type != "result" || result.IsError || result.FromReasoning {
		if t.ModuleID == "evening_briefing" {
			return eveningFallback(context.Background(), t)
		}
		return heartbeat.Execution{}, errors.New("Agent did not produce a successful final answer")
	}
	if t.ModuleID == "evening_briefing" {
		text, e := (assistant.EveningService{Home: config.MemoryUserDir()}).Finalize(ctx, t, result.Result, false)
		if e != nil {
			return eveningFallback(ctx, t)
		}
		return heartbeat.Execution{Text: text, SessionID: result.SessionID, DecisionReason: "23:00睡前总结"}, nil
	}
	if err := verifyHeartbeatEvidence(ctx, t, result.Result); err != nil {
		return heartbeat.Execution{}, err
	}
	if t.ModuleID == "ai_news_briefing" {
		text, err := (assistant.HotNewsService{Home: config.MemoryUserDir()}).Finalize(ctx, t, result.Result)
		if err != nil {
			return heartbeat.Execution{}, err
		}
		return heartbeat.Execution{Text: text, SessionID: result.SessionID, DecisionReason: "AI News七条事件摘要"}, nil
	}
	if t.ModuleID == "morning_briefing" {
		text, err := (assistant.MorningService{Home: config.MemoryUserDir()}).Finalize(ctx, t, result.Result)
		if err != nil {
			return heartbeat.Execution{}, err
		}
		return heartbeat.Execution{Text: text, SessionID: result.SessionID, DecisionReason: "早安Friday"}, nil
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
	if t.Capability == "mail" && t.ModuleID != "mail_digest" {
		var e error
		text, e = assistant.AppendMailProposalQuestions(ctx, config.MemoryUserDir(), t, text)
		if e != nil {
			return heartbeat.Execution{}, e
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
	if t.Capability == "mail" || t.Capability == "weather" || t.Capability == "course" || t.Capability == "morning" || t.Capability == "news" || t.Capability == "evening" {
		steps := "24"
		if t.ModuleID == "mail_digest" || t.ModuleID == "morning_briefing" || t.ModuleID == "ai_news_briefing" {
			steps = "64"
		}
		args = append(args, "--max-steps", steps)
	}
	prompt := t.Prompt
	if t.DecideNotification {
		switch {
		case t.ModuleID == "evening_briefing":
		case t.ModuleID == "ai_news_briefing":
			prompt += assistant.NewsBriefingContract
		case t.ModuleID == "morning_briefing":
			prompt += assistant.MorningContract
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

func heartbeatModelTimeout(t heartbeat.Task) time.Duration {
	if t.ModuleID == "evening_briefing" {
		return 90 * time.Second
	}
	return 10 * time.Minute
}
func eveningFallback(ctx context.Context, t heartbeat.Task) (heartbeat.Execution, error) {
	text, e := (assistant.EveningService{Home: config.MemoryUserDir()}).Finalize(ctx, t, `{}`, true)
	return heartbeat.Execution{Text: text, DecisionReason: "睡前总结使用事实降级"}, e
}

func heartbeatAgentEnv(t heartbeat.Task) []string {
	var env []string
	if t.DecideNotification {
		env = append(os.Environ(), "REASONIX_ASSISTANT_BACKGROUND=1", "REASONIX_ASSISTANT_JOB_ID="+t.ID)
	}
	if t.ModuleID == "evening_briefing" {
		env = append(env, "REASONIX_ASSISTANT_EVENING_BACKGROUND=1", fmt.Sprintf("REASONIX_ASSISTANT_EVENING_ATTEMPT=%d", t.RunStartedAt))
	}
	if t.Capability == "mail" {
		env = append(env, "REASONIX_ASSISTANT_MAIL_BACKGROUND=1", fmt.Sprintf("REASONIX_ASSISTANT_MAIL_ATTEMPT=%d", t.RunStartedAt))
		if t.ModuleID == "mail_digest" {
			env = append(env, "REASONIX_ASSISTANT_MAIL_DIGEST=1")
		}
	}
	if t.Capability == "weather" {
		env = append(env, "REASONIX_ASSISTANT_WEATHER_BACKGROUND=1", fmt.Sprintf("REASONIX_ASSISTANT_WEATHER_ATTEMPT=%d", t.RunStartedAt))
	}
	if t.Capability == "course" {
		env = append(env, "REASONIX_ASSISTANT_COURSE_BACKGROUND=1", fmt.Sprintf("REASONIX_ASSISTANT_COURSE_ATTEMPT=%d", t.RunStartedAt))
	}
	if t.ModuleID == "morning_briefing" {
		env = append(env, "REASONIX_ASSISTANT_MORNING_BACKGROUND=1", fmt.Sprintf("REASONIX_ASSISTANT_MORNING_ATTEMPT=%d", t.RunStartedAt), fmt.Sprintf("REASONIX_ASSISTANT_MAIL_ATTEMPT=%d", t.RunStartedAt), fmt.Sprintf("REASONIX_ASSISTANT_WEATHER_ATTEMPT=%d", t.RunStartedAt))
	}
	if t.ModuleID == "ai_news_briefing" {
		env = append(env, "REASONIX_ASSISTANT_NEWS_BACKGROUND=1", fmt.Sprintf("REASONIX_ASSISTANT_NEWS_ATTEMPT=%d", t.RunStartedAt))
	}
	return env
}

func prepareHeartbeatSources(ctx context.Context, t heartbeat.Task) (heartbeat.Task, error) {
	if t.ModuleID == "evening_briefing" {
		if e := (assistant.EveningService{Home: config.MemoryUserDir()}).Prepare(ctx, t); e != nil {
			return t, e
		}
	}
	t, e := prepareMorningTask(ctx, t)
	if e != nil {
		return t, e
	}
	return t, prepareStandaloneNews(ctx, t)
}
