package cli

import (
	"context"
	"encoding/json"
	"errors"
	"reasonix/internal/assistant"
	"reasonix/internal/config"
	"reasonix/internal/heartbeat"
	"strings"
)

func prepareMorningTask(ctx context.Context, t heartbeat.Task) (heartbeat.Task, error) {
	if t.ModuleID != "morning_briefing" {
		return t, nil
	}
	if _, err := (assistant.MorningService{Home: config.MemoryUserDir()}).Prepare(ctx, t); err != nil {
		return t, err
	}
	jobs, err := (assistant.JobStore{Repository: assistant.NewRepository(config.MemoryUserDir())}).List(ctx)
	if err != nil {
		return t, err
	}
	for _, job := range jobs {
		if job.ID == t.ID {
			job.RunStartedAt = t.RunStartedAt
			return job, nil
		}
	}
	return t, errors.New("morning job disappeared during preparation")
}

func verifyHeartbeatEvidence(ctx context.Context, t heartbeat.Task, raw string) error {
	switch t.Capability {
	case "mail":
		return assistant.NewMailService(config.MemoryUserDir()).VerifyMailReads(ctx, t.ID, t.RunStartedAt, t.MailIDs)
	case "course":
		return (assistant.CourseService{Home: config.MemoryUserDir()}).VerifyBriefing(ctx, t, raw)
	case "weather":
		if err := assistant.NewWeatherService(config.MemoryUserDir()).VerifyWeatherRead(ctx, t.ID, t.RunStartedAt); err != nil {
			return err
		}
		var v struct {
			Notify  bool   `json:"notify"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(raw), &v) != nil || !v.Notify || strings.TrimSpace(v.Message) == "" {
			return errors.New("detailed weather requires a nonempty notification")
		}
	}
	return nil
}
