package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reasonix/internal/assistant"
	"reasonix/internal/config"
	"time"
)

func assistantCommand(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Println("reasonix assistant status|tasks|memories|context\nPersonal state is scoped to REASONIX_HOME. Manage tasks and memories through assistant_tasks / assistant_memory in the owner conversation.")
		return 0
	}
	var result any
	var err error
	home := config.MemoryUserDir()
	switch args[0] {
	case "status":
		result = map[string]any{"enabled": assistant.Enabled(), "home": home, "database": assistant.NewRepository(home).Path, "now": time.Now().Format(time.RFC3339)}
	case "tasks":
		result, err = (assistant.Tasks{Repository: assistant.NewRepository(home)}).List(context.Background(), true)
	case "memories":
		result, err = (assistant.Memories{Home: home}).Apply(context.Background(), assistant.MemoryRequest{Action: "list", IncludeInactive: true})
	case "context":
		result, err = assistant.ContextFor(context.Background(), home, time.Now())
	default:
		err = errors.New("expected status, tasks, memories or context")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "assistant:", err)
		return 1
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
