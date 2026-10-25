package cli

import (
	"context"
	"encoding/json"
	"reasonix/internal/assistant"
	"reasonix/internal/config"
	"reasonix/internal/heartbeat"
)

func prepareStandaloneNews(ctx context.Context, t heartbeat.Task) error {
	if t.ModuleID != "ai_news_briefing" {
		return nil
	}
	return (assistant.HotNewsService{Home: config.MemoryUserDir()}).Prepare(ctx, t)
}

func assistantNewsCommand(home string, args []string) (any, error) {
	request := `{}`
	if len(args) > 1 {
		request = args[1]
	}
	raw, err := (assistant.NewsTool{Service: assistant.HotNewsService{Home: home}}).Execute(context.Background(), json.RawMessage(request))
	if err != nil {
		return nil, err
	}
	var v any
	err = json.Unmarshal([]byte(raw), &v)
	return v, err
}
