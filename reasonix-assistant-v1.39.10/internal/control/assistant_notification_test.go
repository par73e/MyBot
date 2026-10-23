package control

import (
	"context"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/session"
	"reasonix/internal/sessioncontext"
	"reasonix/internal/tool"
)

func TestAssistantNotificationSurvivesReopenWithoutDuplication(t *testing.T) {
	t.Setenv("REASONIX_ASSISTANT", "1")
	service, err := session.NewService("local", session.NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "daily"})
	if err != nil {
		t.Fatal(err)
	}
	initial := agent.NewSession("Friday")
	host := sessioncontext.Build(sessioncontext.Sections{BackgroundMemory: "PRIVATE_HOST_STATE"}).Content
	initial.Add(provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: host})
	initial.Add(provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "早上好"})
	if err := seedRuntimeSession(t.Context(), runtime, "seed", initial.Snapshot(), "", ""); err != nil {
		t.Fatal(err)
	}
	exec := agent.New(nil, tool.NewRegistry(), initial, agent.Options{}, event.Discard)
	c := newOwnedTestController(t, Options{Executor: exec, SystemPrompt: "Friday", Sink: event.Discard, SessionService: service, SessionRuntime: runtime, ExclusiveSession: true})
	at := time.Now()
	for range 2 {
		if err := c.RecordAssistantNotification(t.Context(), "stable-run", "Sir，这是今日简报", at); err != nil {
			t.Fatal(err)
		}
	}
	check := func() {
		t.Helper()
		count := 0
		for _, m := range runtime.Session().Snapshot().Projection.Messages {
			if m.Content == "Sir，这是今日简报" {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("notification count=%d", count)
		}
		context := c.AssistantConversationContext()
		if !strings.Contains(context, "今日简报") || strings.Contains(context, "PRIVATE_HOST_STATE") {
			t.Fatalf("wrong next-turn context: %s", context)
		}
	}
	check()
	other, err := service.Create(t.Context(), session.CreateOptions{SessionID: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.OpenSession(t.Context(), other.Ref()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.OpenSession(t.Context(), runtime.Ref()); err != nil {
		t.Fatal(err)
	}
	if err := c.RecordAssistantNotification(t.Context(), "stable-run", "Sir，这是今日简报", at); err != nil {
		t.Fatal(err)
	}
	check()
}
