package cli

import (
	"context"
	"fmt"
	"log"
	"time"

	"reasonix/internal/assistant"
	"reasonix/internal/config"
	"reasonix/internal/heartbeat"
	"reasonix/internal/secrets"
)

func (r heartbeatProcessRunner) runGitHubNotification(ctx context.Context, t heartbeat.Task) (heartbeat.Execution, error) {
	service := assistant.GitHubService{Home: config.MemoryUserDir()}
	budget, cancel := context.WithTimeout(ctx, 60*time.Second)
	result, err := r.runAgent(budget, t)
	if err == nil {
		var text string
		text, err = service.FinalizeNotification(budget, t, result.Result, false)
		if err == nil {
			cancel()
			return heartbeat.Execution{Text: text, SessionID: result.SessionID, DecisionReason: "GitHub回复中文摘要"}, nil
		}
	}
	cancel()
	if ctx.Err() != nil {
		return heartbeat.Execution{}, ctx.Err()
	}
	log.Printf("heartbeat: task %s: GitHub summary fallback: %s", t.ID, secrets.Redact(fmt.Sprint(err)))
	fallback, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	text, err := service.FinalizeNotification(fallback, t, "", true)
	return heartbeat.Execution{Text: text, SessionID: result.SessionID, DecisionReason: "GitHub进展使用中文事实提示"}, err
}
