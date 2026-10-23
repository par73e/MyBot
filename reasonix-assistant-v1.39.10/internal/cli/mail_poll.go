package cli

import (
	"context"
	"fmt"
	"os"

	"reasonix/internal/assistant"
	"reasonix/internal/config"
	"reasonix/internal/heartbeat"
)

type mailboxHeartbeatRunner struct {
	service assistant.MailService
	options assistant.Options
}

func (r mailboxHeartbeatRunner) Run(ctx context.Context, t heartbeat.Task) (heartbeat.Execution, error) {
	options := r.options
	options.MailScheduleID = t.ID
	_, err := r.service.Poll(ctx, options)
	return heartbeat.Execution{}, err
}

// Network checks are native Heartbeat work, with no separate ticker or model turn.
func configureMailboxHeartbeat(ctx context.Context, engine *heartbeat.Engine, model, workspace string) error {
	if !assistant.Enabled() {
		return nil
	}
	service := assistant.NewMailService(config.MemoryUserDir())
	engine.Gate = service.ScheduleAllows
	if _, err := service.Config(); err != nil {
		return nil
	}
	options, err := mailPollOptions(model, workspace)
	if err != nil {
		return err
	}
	if _, err = service.EnsureSchedule(ctx, options); err != nil {
		return err
	}
	engine.Checker = mailboxHeartbeatRunner{service, options}
	return nil
}

func mailPollOptions(model, workspace string) (assistant.Options, error) {
	cfg, err := config.LoadUserConfigReadOnly()
	if err != nil {
		return assistant.Options{}, err
	}
	if len(cfg.Bot.Allowlist.WeixinUsers) != 1 {
		return assistant.Options{}, fmt.Errorf("mail polling requires one bound Weixin owner")
	}
	root := workspace
	if root == "" {
		root, err = os.Getwd()
		if err != nil {
			return assistant.Options{}, err
		}
	}
	return assistant.Options{Workspace: root, Model: model, Target: &heartbeat.Target{ConnectionID: "weixin", Domain: "weixin", ChatID: cfg.Bot.Allowlist.WeixinUsers[0], ChatType: "dm"}}, nil
}
