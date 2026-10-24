package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"reasonix/internal/assistant"
	"reasonix/internal/config"
	"reasonix/internal/heartbeat"
)

type botHeartbeatOptions struct {
	workspace string
	enabled   bool
	file      string
	poll      time.Duration
}

func bindBotHeartbeatFlags(fs *flag.FlagSet) *botHeartbeatOptions {
	options := &botHeartbeatOptions{}
	fs.BoolVar(&options.enabled, "heartbeat", false, "同时启用命令行 Heartbeat 定时任务")
	fs.StringVar(&options.file, "heartbeat-file", defaultHeartbeatPath(), "Heartbeat 任务文件")
	fs.DurationVar(&options.poll, "heartbeat-poll", 30*time.Second, "Heartbeat 检查间隔，至少 1s")
	return options
}

func startBotHeartbeat(ctx context.Context, gateway heartbeatGateway, options *botHeartbeatOptions, model string) (func(), error) {
	if !options.enabled {
		return func() {}, nil
	}
	if options.poll < time.Second {
		return nil, errors.New("heartbeat poll must be at least 1s")
	}
	var store heartbeat.TaskStore = heartbeat.Store{Path: options.file}
	if assistant.Enabled() {
		store = assistant.JobStore{Repository: assistant.NewRepository(config.MemoryUserDir())}
	}
	if _, err := store.List(ctx); err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	var runner heartbeat.Runner = heartbeatProcessRunner{Executable: executable, DefaultModel: model}
	if assistant.Enabled() {
		runner = heartbeatConversationRunner{Runner: runner, Gateway: gateway}
	}
	engine := heartbeat.Engine{
		Store: store, Runner: runner,
		Sender: heartbeatGatewaySender{gateway: gateway},
	}
	if assistant.Enabled() {
		if err := configureUnifiedHeartbeat(ctx, &engine, model, options.workspace); err != nil {
			return nil, err
		}
	} else if err := configureMailboxHeartbeat(ctx, &engine, model, options.workspace); err != nil {
		return nil, err
	}
	if !assistant.Enabled() {
		if err := configureWeatherHeartbeat(ctx, &engine, model, options.workspace); err != nil {
			return nil, err
		}
	}
	engine.Sender = heartbeatGatewaySender{gateway: gateway, admit: engine.Admitted}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = engine.StartLanes(workerCtx, options.poll, func(err error) { fmt.Fprintln(os.Stderr, "heartbeat:", err) })
	}()
	return func() { cancel(); <-done }, nil
}
