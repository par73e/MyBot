package cli

import (
	"context"
	"encoding/json"
	"os"

	"reasonix/internal/assistant"
)

func assistantAlertCommand(home string, args []string) (any, error) {
	s := assistant.AlertService{Home: home}
	if args[0] == "weather-alerts-check" {
		workspace, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		err = s.Poll(context.Background(), assistant.Options{Workspace: workspace})
		return map[string]any{"checked": err == nil, "notifications_queued": false}, err
	}
	raw := `{"action":"status"}`
	if len(args) > 1 {
		raw = args[1]
	}
	text, err := (assistant.AlertTool{Service: s}).Execute(context.Background(), json.RawMessage(raw))
	if err != nil {
		return nil, err
	}
	var out any
	err = json.Unmarshal([]byte(text), &out)
	return out, err
}
