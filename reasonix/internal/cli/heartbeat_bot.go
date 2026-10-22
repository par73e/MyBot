package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"reasonix/internal/heartbeat"
)

type botHeartbeatOptions struct {
	enabled bool
	file    string
	poll    time.Duration
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
	store := heartbeat.Store{Path: options.file}
	if _, err := store.List(ctx); err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	engine := heartbeat.Engine{
		Store: store, Runner: heartbeatProcessRunner{Executable: executable, DefaultModel: model},
		Sender: heartbeatGatewaySender{gateway: gateway},
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = engine.Start(workerCtx, options.poll, func(err error) { fmt.Fprintln(os.Stderr, "heartbeat:", err) })
	}()
	return func() { cancel(); <-done }, nil
}
