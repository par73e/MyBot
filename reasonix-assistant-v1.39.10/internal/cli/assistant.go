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
		fmt.Println("reasonix assistant status|tasks|memories|context|mail|mail-schedule|weather|courses|courses-import|subscriptions|evening|expansion-enable\nPersonal state is scoped to REASONIX_HOME. Manage tasks and memories through assistant_tasks / assistant_memory in the owner conversation.")
		return 0
	}
	var result any
	var err error
	home := config.MemoryUserDir()
	switch args[0] {
	case "mail-delivery-repair":
		var request assistant.MailDeliveryRepair
		if len(args) != 2 {
			err = errors.New("mail-delivery-repair requires an audited JSON request; apply defaults false")
		} else if err = json.Unmarshal([]byte(args[1]), &request); err == nil {
			result, err = assistant.NewMailService(home).RepairDelivery(context.Background(), request)
		}
	case "weather-alerts", "weather-alerts-check":
		result, err = assistantAlertCommand(home, args)
	case "github", "github-check":
		result, err = assistantGitHubCommand(home, args)
	case "courses", "courses-import":
		result, err = assistantCourseCommand(home, args)
	case "news":
		result, err = assistantNewsCommand(home, args)
	case "expansion-enable", "subscriptions", "evening":
		result, err = assistantExpansionCommand(home, args)
	case "morning-enable":
		result, err = (assistant.HeartbeatService{Home: home}).EnableMorning(context.Background())
	case "heartbeat-init":
		result, err = (assistant.HeartbeatService{Home: home}).Ensure(context.Background())
	case "heartbeat":
		request := `{"action":"get"}`
		if len(args) > 1 {
			request = args[1]
		}
		var raw string
		raw, err = (assistant.HeartbeatTool{Service: assistant.HeartbeatService{Home: home}}).Execute(context.Background(), json.RawMessage(request))
		if err == nil {
			err = json.Unmarshal([]byte(raw), &result)
		}
	case "memory":
		result, err = assistantMemoryCommand(home, args)
	case "weather":
		request := assistant.WeatherRequest{Action: "status"}
		if len(args) > 1 {
			err = json.Unmarshal([]byte(args[1]), &request)
		}
		if err == nil {
			result, err = assistant.NewWeatherService(home).Query(context.Background(), request)
		}
	case "mail":
		request := assistant.MailRequest{Action: "status"}
		if len(args) > 1 {
			err = json.Unmarshal([]byte(args[1]), &request)
		}
		if err == nil {
			result, err = assistant.NewMailService(home).Query(context.Background(), request)
		}
	case "mail-schedule":
		request := `{"action":"get"}`
		if len(args) > 1 {
			request = args[1]
		}
		var raw string
		raw, err = (assistant.MailScheduleTool{Service: assistant.NewMailService(home)}).Execute(context.Background(), json.RawMessage(request))
		if err == nil {
			err = json.Unmarshal([]byte(raw), &result)
		}
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
