package control

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/assistant"
	"reasonix/internal/config"
)

func (c *Controller) recordCompletedNewsTurn(ctx context.Context, start int) {
	if !assistant.Enabled() || ctx.Err() != nil || c.CancelRequested() || c.executor == nil || os.Getenv("REASONIX_ASSISTANT_NEWS_BACKGROUND") == "1" {
		return
	}
	source := ""
	if os.Getenv("ASROBOT_FOREGROUND_SOURCE") == "web" {
		source = "web"
	} else if strings.HasPrefix(c.parentSessionID(), "friday-") {
		source = "weixin"
	}
	if source == "" {
		return
	}
	messages := c.executor.Session().Snapshot()
	if start < 0 || start >= len(messages) {
		return
	}
	messages[len(messages)-1].Content = agent.DisplayAssistantText(messages[len(messages)-1].Content)
	if err := (assistant.HotNewsService{Home: config.ReasonixHomeDir()}).RecordCompletedNewsTurn(ctx, source, c.parentSessionID(), messages[start:]); err != nil {
		slog.Warn("assistant: completed news report could not be indexed", "err", err)
	}
}
