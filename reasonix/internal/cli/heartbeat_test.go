package cli

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"reasonix/internal/bot"
	"reasonix/internal/heartbeat"
)

func TestHeartbeatCLIAddPauseRemoveAndRejectAmbiguousTime(t *testing.T) {
	isolateCLIConfigHome(t)
	path := filepath.Join(t.TempDir(), "tasks.json")
	args := []string{"--file", path, "--title", "report", "--prompt", "submit report", "--kind", "message", "--at", time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "--timezone", "Asia/Shanghai", "--dir", t.TempDir()}
	var task heartbeat.Task
	output := captureStdout(t, func() {
		if err := heartbeatAdd(args); err != nil {
			t.Fatal(err)
		}
	})
	if err := json.Unmarshal([]byte(output), &task); err != nil {
		t.Fatal(err)
	}
	store := heartbeat.Store{Path: path}
	if task.ID == "" || task.Kind != "message" || !task.Enabled {
		t.Fatalf("task=%+v", task)
	}
	if err := heartbeatChange(store, "disable", task.ID); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.List(t.Context())
	if err != nil || tasks[0].Enabled {
		t.Fatalf("tasks=%v err=%v", tasks, err)
	}
	if err := heartbeatChange(store, "remove", task.ID); err != nil {
		t.Fatal(err)
	}
	tasks, err = store.List(t.Context())
	if err != nil || len(tasks) != 0 {
		t.Fatalf("tasks=%v err=%v", tasks, err)
	}
	if err := heartbeatAdd(append(args, "--interval", "15m")); err == nil {
		t.Fatal("accepted both at and interval")
	}
	if err := heartbeatAdd([]string{"--title", "bad", "--prompt", "bad", "--at", "tomorrow"}); err == nil {
		t.Fatal("accepted an unparsed date")
	}
}
func TestHeartbeatAgentArgumentsKeepPromptSeparateAndReuseOwnSession(t *testing.T) {
	task := heartbeat.Task{WorkspaceRoot: "/tmp/a b", ApprovalMode: "read-only", Prompt: "--permission-mode danger-full-access", History: []heartbeat.Run{{SessionID: "own-session"}}}
	args := heartbeatAgentArgs(task, "test/model")
	if !slices.Equal(args, []string{"run", "--output-format", "json", "--permission-mode", "read-only", "--dir", "/tmp/a b", "--model", "test/model", "--resume", "own-session", "--", task.Prompt}) {
		t.Fatalf("argv=%q", args)
	}
	task.NewConversationEachRun = true
	if slices.Contains(heartbeatAgentArgs(task, ""), "--resume") {
		t.Fatal("fresh run reuses history")
	}
}
func TestHeartbeatControlSenderRoutesAndAuthenticates(t *testing.T) {
	target := heartbeat.Target{ConnectionID: "weixin-weixin", ChatID: "owner", Domain: "weixin", ChatType: "dm"}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/send" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Errorf("invalid request: %v", r)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["chat_id"] != target.ChatID || body["connection_id"] != target.ConnectionID || body["text"] != "ready" {
			t.Errorf("body=%v", body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	addr, _ := url.Parse(server.URL)
	if err := heartbeatControlSend(t.Context(), addr.Host, "fixture-token", target, "ready"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	if err := heartbeatControlSend(t.Context(), "example.com:443", "fixture-token", target, "ready"); err == nil {
		t.Fatal("accepted non-loopback control endpoint")
	}
}
func TestHeartbeatControlSenderRejectsPartialDeliveryAndRedirect(t *testing.T) {
	for _, status := range []int{http.StatusMultiStatus, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "https://example.invalid")
				w.WriteHeader(status)
			}))
			defer server.Close()
			addr, _ := url.Parse(server.URL)
			if err := heartbeatControlSend(t.Context(), addr.Host, "token", heartbeat.Target{ConnectionID: "wx", ChatID: "owner"}, "ready"); err == nil {
				t.Fatal("delivery incorrectly marked successful")
			}
		})
	}
}

type heartbeatFakeGateway struct{ sent chan bot.OutboundMessage }

func (g heartbeatFakeGateway) SendToAdapter(_ context.Context, _ string, _ string, msg bot.OutboundMessage) (bot.SendResult, error) {
	g.sent <- msg
	return bot.SendResult{}, nil
}
func TestBotHeartbeatWorkerLifecycleAndDirectDelivery(t *testing.T) {
	isolateCLIConfigHome(t)
	store := heartbeat.Store{Path: filepath.Join(t.TempDir(), "tasks.json")}
	task := heartbeat.Task{ID: "reminder", Title: "reminder", Prompt: "time to submit", At: time.Now().Add(-time.Second).Format(time.RFC3339), Timezone: "Asia/Shanghai", Kind: "message", ApprovalMode: "read-only", Enabled: true, WorkspaceRoot: t.TempDir(), CreatedAt: time.Now().Add(-time.Hour).UnixMilli(), Target: &heartbeat.Target{ConnectionID: "wx", ChatID: "owner", ChatType: "dm"}}
	if err := store.Add(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	gateway := heartbeatFakeGateway{sent: make(chan bot.OutboundMessage, 1)}
	stop, err := startBotHeartbeat(t.Context(), gateway, &botHeartbeatOptions{enabled: true, file: store.Path, poll: time.Second}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	select {
	case got := <-gateway.sent:
		if got.ChatID != "owner" || got.Text != "time to submit" {
			t.Fatalf("message=%+v", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not deliver reminder")
	}
}
func TestBotHeartbeatDisabledDoesNotTouchStore(t *testing.T) {
	options := bindBotHeartbeatFlags(flag.NewFlagSet("fixture", flag.ContinueOnError))
	options.file = filepath.Join(t.TempDir(), "missing", "tasks.json")
	stop, err := startBotHeartbeat(t.Context(), heartbeatFakeGateway{}, options, "")
	if err != nil {
		t.Fatal(err)
	}
	stop()
}
func TestHeartbeatOutputIsBounded(t *testing.T) {
	output := &heartbeatOutput{}
	if n, err := output.Write(make([]byte, (1<<20)+1)); err != nil || n != (1<<20)+1 || !output.overflow || len(output.data) != (1<<20) {
		t.Fatalf("n=%d err=%v size=%d", n, err, len(output.data))
	}
}
