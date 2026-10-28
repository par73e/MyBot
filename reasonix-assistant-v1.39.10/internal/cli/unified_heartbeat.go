package cli

import (
	"context"
	"reasonix/internal/assistant"
	"reasonix/internal/config"
	"reasonix/internal/heartbeat"
)

func configureUnifiedHeartbeat(ctx context.Context, e *heartbeat.Engine, model, workspace string) error {
	o, err := mailPollOptions(model, workspace)
	if err != nil {
		return err
	}
	service := assistant.HeartbeatService{Home: config.MemoryUserDir(), Options: o}
	if _, err = service.Ensure(ctx); err != nil {
		return err
	}
	e.Prepare = service.Prepare
	e.AfterDelivery = func(ctx context.Context, t heartbeat.Task, r heartbeat.Run) error {
		if err := assistant.NewMailService(config.MemoryUserDir()).AcknowledgeNotification(ctx, t, r); err != nil {
			return err
		}
		if err := (assistant.AlertService{Home: config.MemoryUserDir()}).Acknowledge(ctx, t, r); err != nil {
			return err
		}
		if err := (assistant.GitHubService{Home: config.MemoryUserDir()}).Acknowledge(ctx, t, r); err != nil {
			return err
		}
		if err := (assistant.EveningService{Home: config.MemoryUserDir()}).Acknowledge(ctx, t, r); err != nil {
			return err
		}
		if err := (assistant.MorningService{Home: config.MemoryUserDir()}).Acknowledge(ctx, t, r); err != nil {
			return err
		}
		if err := (assistant.HotNewsService{Home: config.MemoryUserDir()}).Acknowledge(ctx, t, r); err != nil {
			return err
		}
		return assistant.NewMailService(config.MemoryUserDir()).AcknowledgeDigest(ctx, t, r)
	}
	e.Gate = service.Allows
	e.Checker = nativeHeartbeatRunner{mail: mailboxHeartbeatRunner{service: assistant.NewMailService(config.MemoryUserDir()), options: o}, github: assistant.GitHubService{Home: config.MemoryUserDir()}, alerts: assistant.AlertService{Home: config.MemoryUserDir()}, options: o}
	return nil
}
