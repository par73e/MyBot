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
	e.AfterDelivery = assistant.NewMailService(config.MemoryUserDir()).AcknowledgeDigest
	e.Gate = service.Allows
	e.Checker = mailboxHeartbeatRunner{service: assistant.NewMailService(config.MemoryUserDir()), options: o}
	return nil
}
