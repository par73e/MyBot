package cli

import (
	"context"
	"encoding/json"
	"errors"

	"reasonix/internal/assistant"
)

func assistantMemoryCommand(home string, args []string) (any, error) {
	if len(args) != 2 {
		return nil, errors.New("memory requires one JSON request")
	}
	var request assistant.MemoryRequest
	if err := json.Unmarshal([]byte(args[1]), &request); err != nil {
		return nil, err
	}
	return (assistant.Memories{Home: home}).Apply(context.Background(), request)
}
