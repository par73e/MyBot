package control

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/session"
	"reasonix/internal/tool"
)

func TestPersonalAssistantReopenRefreshesOnlyModelPersona(t *testing.T) {
	for _, enabled := range []string{"", "1"} {
		t.Run("assistant="+enabled, func(t *testing.T) {
			t.Setenv("REASONIX_ASSISTANT", enabled)
			service, err := session.NewService("local", session.NewFilesystemPersistence(filepath.Join(t.TempDir(), "sessions")))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
			first, err := service.Create(t.Context(), session.CreateOptions{SessionID: "first"})
			if err != nil {
				t.Fatal(err)
			}
			old := agent.NewSession("old coding identity")
			old.Add(provider.Message{Role: provider.RoleUser, Content: "old question"})
			old.Add(provider.Message{Role: provider.RoleAssistant, Content: "old answer"})
			exec := agent.New(nil, tool.NewRegistry(), old, agent.Options{}, event.Discard)
			c := newOwnedTestController(t, Options{Executor: exec, SystemPrompt: "old coding identity", Sink: event.Discard, SessionService: service, SessionRuntime: first, ExclusiveSession: true})
			before := first.Session().ExecutionSnapshot().Projection.Messages
			c.prompt.base = "Friday personal assistant"
			other, err := service.Create(t.Context(), session.CreateOptions{SessionID: "other"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.OpenSession(t.Context(), other.Ref()); err != nil {
				t.Fatal(err)
			}
			if _, err := c.OpenSession(t.Context(), first.Ref()); err != nil {
				t.Fatal(err)
			}
			got := exec.Session().Snapshot()
			wantPrompt := "old coding identity"
			if enabled == "1" {
				wantPrompt = "Friday personal assistant"
			}
			if got[0].Content != wantPrompt || !reflect.DeepEqual(got[1:], old.Snapshot()[1:]) {
				t.Fatalf("reopened model context changed incorrectly: %+v", got)
			}
			if !reflect.DeepEqual(first.Session().ExecutionSnapshot().Projection.Messages, before) {
				t.Fatal("persona refresh rewrote the visible transcript")
			}
		})
	}
}
