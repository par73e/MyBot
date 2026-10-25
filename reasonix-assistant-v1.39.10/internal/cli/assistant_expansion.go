package cli

import (
	"context"
	"encoding/json"
	"errors"
	"reasonix/internal/assistant"
	"reasonix/internal/config"
)

func assistantExpansionCommand(home string, args []string) (any, error) {
	switch args[0] {
	case "expansion-enable":
		cfg, e := config.LoadUserConfigReadOnly()
		if e != nil {
			return nil, e
		}
		o, e := mailPollOptions(cfg.DefaultModel, "")
		if e != nil {
			return nil, e
		}
		return (assistant.HeartbeatService{Home: home, Options: o}).EnableExpansion(context.Background())
	case "subscriptions":
		var r assistant.SubscriptionRequest
		if len(args) != 2 {
			return nil, errors.New("subscriptions requires a JSON request")
		}
		if e := json.Unmarshal([]byte(args[1]), &r); e != nil {
			return nil, e
		}
		return (assistant.Subscriptions{Repository: assistant.NewRepository(home)}).Apply(context.Background(), r)
	case "evening":
		request := `{"action":"preview"}`
		if len(args) > 1 {
			request = args[1]
		}
		raw, e := (assistant.EveningTool{Service: assistant.EveningService{Home: home}}).Execute(context.Background(), json.RawMessage(request))
		if e != nil {
			return nil, e
		}
		var result any
		e = json.Unmarshal([]byte(raw), &result)
		return result, e
	}
	return nil, errors.New("unknown expansion command")
}
