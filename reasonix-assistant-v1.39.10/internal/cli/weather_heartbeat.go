package cli

import (
	"context"
	"errors"
	"time"

	"reasonix/internal/assistant"
	"reasonix/internal/config"
	"reasonix/internal/heartbeat"
)

type weatherHeartbeatRunner struct {
	service assistant.WeatherService
	options assistant.Options
	other   heartbeat.Runner
}

func (r weatherHeartbeatRunner) Run(ctx context.Context, t heartbeat.Task) (heartbeat.Execution, error) {
	if t.Kind == "weather_check" && t.ModuleID != "weather_alert_check" {
		return heartbeat.Execution{}, r.service.MorningTick(ctx, r.options)
	}
	if r.other == nil {
		return heartbeat.Execution{}, errors.New("no checker registered for task kind")
	}
	return r.other.Run(ctx, t)
}
func configureWeatherHeartbeat(ctx context.Context, engine *heartbeat.Engine, model, workspace string) error {
	if !assistant.Enabled() {
		return nil
	}
	service := assistant.NewWeatherService(config.MemoryUserDir())
	if _, err := service.Config(); err != nil {
		return nil
	}
	options, err := mailPollOptions(model, workspace)
	if err != nil {
		return err
	}
	if err = service.EnsureMorning(ctx, options); err != nil {
		return err
	}
	engine.Checker = weatherHeartbeatRunner{service: service, options: options, other: engine.Checker}
	previous := engine.Gate
	engine.Gate = func(ctx context.Context, t heartbeat.Task, now time.Time) (bool, error) {
		if previous != nil {
			allowed, err := previous(ctx, t, now)
			if err != nil || !allowed {
				return allowed, err
			}
		}
		return service.MorningAllows(ctx, t, now)
	}
	return nil
}
