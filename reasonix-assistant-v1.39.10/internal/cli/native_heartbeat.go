package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"reasonix/internal/assistant"
	"reasonix/internal/heartbeat"
)

type nativeHeartbeatRunner struct {
	mail    mailboxHeartbeatRunner
	github  assistant.GitHubService
	alerts  assistant.AlertService
	options assistant.Options
}

func (r nativeHeartbeatRunner) Run(ctx context.Context, t heartbeat.Task) (heartbeat.Execution, error) {
	if t.Kind == "weather_check" && t.ModuleID == "weather_alert_check" {
		return heartbeat.Execution{}, r.alerts.Poll(ctx, r.options)
	}
	if t.Kind == "mail_check" {
		return r.mail.Run(ctx, t)
	}
	if t.Kind == "module_check" && t.ModuleID == "github_pr_check" {
		return heartbeat.Execution{}, r.github.Poll(ctx, r.options)
	}
	return heartbeat.Execution{}, errors.New("unsupported native Heartbeat module")
}

func assistantGitHubCommand(home string, args []string) (any, error) {
	if args[0] == "github-check" {
		workspace, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		err = (assistant.GitHubService{Home: home}).Poll(context.Background(), assistant.Options{Workspace: workspace})
		return map[string]any{"checked": err == nil, "notifications_queued": false}, err
	}
	raw := `{"action":"status"}`
	if len(args) > 1 {
		raw = args[1]
	}
	text, err := (assistant.GitHubTool{Service: assistant.GitHubService{Home: home}}).Execute(context.Background(), json.RawMessage(raw))
	if err != nil {
		return nil, err
	}
	var result any
	err = json.Unmarshal([]byte(text), &result)
	return result, err
}
