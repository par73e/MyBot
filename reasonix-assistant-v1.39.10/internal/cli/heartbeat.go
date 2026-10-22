package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/heartbeat"
)

const heartbeatHelp = `reasonix heartbeat — headless scheduled Agent tasks and reminders

Usage:
  reasonix heartbeat add --title TEXT --prompt TEXT (--interval RULE | --at RFC3339) [options]
  reasonix heartbeat list [--file PATH]
  reasonix heartbeat enable|disable|remove --id ID [--file PATH]
  reasonix heartbeat start [--poll 30s] [--file PATH]
  reasonix heartbeat tick [--file PATH]
  reasonix heartbeat run --id ID [--file PATH]

Options for add:
  --kind agent|message       Agent analysis (default) or a zero-token fixed message
  --timezone IANA_ZONE       Time zone for recurring calendar rules (default Local)
  --ends-at RFC3339          Exclusive expiry boundary
  --dir PATH                Existing workspace (default current directory)
  --model NAME              Optional provider/model override
  --permission-mode MODE    read-only (default) or workspace-write
  --reuse-session           Continue this task's own previous Agent conversation
  --window-start HH:MM      Interval execution window, inclusive
  --window-end HH:MM        Interval execution window, exclusive
  --connection ID --chat ID Explicit Bot notification target (otherwise store result only)
  --domain NAME --chat-type dm|group  Optional Bot routing fields

Rules: 15m; "0 8 * * 1-5"; "24h|daily@08:00"; "168h|weekly:mon,wed@08:00".
Each command accepts --file. The default is REASONIX_HOME/heartbeat-cli.json.
Use bot start --heartbeat to run the worker alongside the connected Bot.
Standalone workers use the authenticated loopback Bot control API for delivery.
`

func runBotOrHeartbeat(command string, args []string, version string) int {
	if command == "assistant" {
		return assistantCommand(args)
	}
	if command == "heartbeat" {
		return heartbeatCommand(args, version)
	}
	configureCLIThemeFromConfig()
	return botCommand(args, version)
}

func heartbeatCommand(args []string, version string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(heartbeatHelp)
		return 0
	}
	if err := runHeartbeatCommand(args, version); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(os.Stderr, "heartbeat:", err)
		return 1
	}
	return 0
}

func runHeartbeatCommand(args []string, version string) error {
	if args[0] == "add" {
		return heartbeatAdd(args[1:])
	}
	fs := flag.NewFlagSet("heartbeat "+args[0], flag.ContinueOnError)
	path := fs.String("file", defaultHeartbeatPath(), "headless task file")
	id := fs.String("id", "", "task ID")
	poll := fs.Duration("poll", 30*time.Second, "schedule polling interval")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	store := heartbeat.Store{Path: *path}
	ctx := context.Background()
	switch args[0] {
	case "list":
		tasks, err := store.List(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(tasks)
	case "enable", "disable", "remove":
		return heartbeatChange(store, args[0], *id)
	case "start", "tick", "run":
		return heartbeatWork(args[0], *id, *poll, store, version)
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func defaultHeartbeatPath() string {
	return filepath.Join(config.MemoryUserDir(), "heartbeat-cli.json")
}

func heartbeatChange(store heartbeat.Store, operation, id string) error {
	if id == "" {
		return errors.New("--id is required")
	}
	if operation != "remove" {
		return store.Update(context.Background(), id, func(t *heartbeat.Task) error {
			t.Enabled = operation == "enable"
			return nil
		})
	}
	return store.Edit(context.Background(), func(tasks *[]heartbeat.Task) error {
		index := slices.IndexFunc(*tasks, func(t heartbeat.Task) bool { return t.ID == id })
		if index < 0 {
			return fmt.Errorf("task %q not found", id)
		}
		*tasks = slices.Delete(*tasks, index, index+1)
		return nil
	})
}

func heartbeatAdd(args []string) error {
	fs := flag.NewFlagSet("heartbeat add", flag.ContinueOnError)
	path := fs.String("file", defaultHeartbeatPath(), "headless task file")
	t := heartbeat.Task{Enabled: true, NewConversationEachRun: true, CreatedAt: time.Now().UnixMilli()}
	bindHeartbeatTaskFlags(fs, &t)
	var target heartbeat.Target
	fs.StringVar(&target.ConnectionID, "connection", "", "Bot connection ID")
	fs.StringVar(&target.ChatID, "chat", "", "Bot chat ID")
	fs.StringVar(&target.Domain, "domain", "", "Bot domain")
	fs.StringVar(&target.ChatType, "chat-type", "dm", "Bot chat type")
	reuse := fs.Bool("reuse-session", false, "reuse this task's own Agent session")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if target.ConnectionID != "" || target.ChatID != "" {
		t.Target = &target
	}
	t.NewConversationEachRun = !*reuse
	var err error
	t.WorkspaceRoot, err = filepath.Abs(t.WorkspaceRoot)
	if err != nil {
		return err
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	t.ID = hex.EncodeToString(b)
	if t.At != "" {
		at, err := time.Parse(time.RFC3339, t.At)
		if err != nil || !at.After(time.Now()) {
			return errors.New("--at must be a future RFC3339 timestamp with offset")
		}
	}
	if err := (heartbeat.Store{Path: *path}).Add(context.Background(), t); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(t)
}

func bindHeartbeatTaskFlags(fs *flag.FlagSet, t *heartbeat.Task) {
	fs.StringVar(&t.Title, "title", "", "task title")
	fs.StringVar(&t.Prompt, "prompt", "", "Agent prompt or fixed message")
	fs.StringVar(&t.Interval, "interval", "", "interval or calendar rule")
	fs.StringVar(&t.At, "at", "", "one-shot RFC3339 timestamp")
	fs.StringVar(&t.Timezone, "timezone", "Local", "calendar time zone")
	fs.StringVar(&t.EndsAt, "ends-at", "", "exclusive expiry timestamp")
	fs.StringVar(&t.WorkspaceRoot, "dir", ".", "Agent workspace")
	fs.StringVar(&t.Model, "model", "", "provider/model override")
	fs.StringVar(&t.ApprovalMode, "permission-mode", "read-only", "unattended permission preset")
	fs.StringVar(&t.Kind, "kind", "agent", "agent or message")
	fs.StringVar(&t.TimeWindowStart, "window-start", "", "interval window start")
	fs.StringVar(&t.TimeWindowEnd, "window-end", "", "interval window end")
}

func heartbeatWork(operation, id string, poll time.Duration, store heartbeat.Store, version string) error {
	if operation == "run" && id == "" {
		return errors.New("--id is required")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	engine := heartbeat.Engine{Store: store, Runner: heartbeatProcessRunner{Executable: executable}, Clock: time.Now}
	engine.Sender = heartbeatControlSender{}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	switch operation {
	case "tick":
		return engine.Tick(ctx, time.Now())
	case "run":
		return engine.Trigger(ctx, id, time.Now())
	default:
		fmt.Fprintf(os.Stderr, "heartbeat worker %s (%s, poll=%s)\n", version, store.Path, poll)
		return engine.Start(ctx, poll, func(err error) { fmt.Fprintln(os.Stderr, "heartbeat:", err) })
	}
}
