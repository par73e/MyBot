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
	"reasonix/internal/sessioncontext"
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
			if err := seedRuntimeSession(t.Context(), first, "seed-old", old.Snapshot(), "", ""); err != nil {
				t.Fatal(err)
			}
			exec := agent.New(nil, tool.NewRegistry(), old, agent.Options{}, event.Discard)
			c := newOwnedTestController(t, Options{Executor: exec, SystemPrompt: "old coding identity", Sink: event.Discard, SessionService: service, SessionRuntime: first, ExclusiveSession: true})
			before := first.Session().Snapshot().Projection.Messages
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
			if !reflect.DeepEqual(first.Session().Snapshot().Projection.Messages, before) {
				t.Fatal("persona refresh rewrote the visible transcript")
			}
		})
	}
}

func TestPersonalAssistantReopenSanitizesOnlyHostDeployment(t *testing.T) {
	for _, enabled := range []string{"", "1"} {
		t.Run("assistant="+enabled, func(t *testing.T) {
			t.Setenv("REASONIX_ASSISTANT", enabled)
			service, err := session.NewService("local", session.NewFilesystemPersistence(t.TempDir()))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
			first, err := service.Create(t.Context(), session.CreateOptions{SessionID: "first"})
			if err != nil {
				t.Fatal(err)
			}
			snapshot := sessioncontext.Build(sessioncontext.Sections{Environment: "git docker python3", Workspace: "/deployment/workspace", BackgroundMemory: "keep this personal memory", SkillsCatalog: "keep this skill"})
			old := agent.NewSession("Friday")
			old.Add(provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: snapshot.Content})
			old.Add(provider.Message{Role: provider.RoleUser, Content: snapshot.Content})
			old.Add(provider.Message{Role: provider.RoleAssistant, Content: "old reply: /deployment/workspace"})
			if err := seedRuntimeSession(t.Context(), first, "seed-old", old.Snapshot(), "", ""); err != nil {
				t.Fatal(err)
			}
			exec := agent.New(nil, tool.NewRegistry(), old, agent.Options{}, event.Discard)
			c := newOwnedTestController(t, Options{Executor: exec, SystemPrompt: "Friday", Sink: event.Discard, SessionService: service, SessionRuntime: first, ExclusiveSession: true})
			before := first.Session().Snapshot().Projection.Messages
			other, err := service.Create(t.Context(), session.CreateOptions{SessionID: "other"})
			if err != nil {
				t.Fatal(err)
			}
			for _, ref := range []session.SessionRef{other.Ref(), first.Ref()} {
				if _, err := c.OpenSession(t.Context(), ref); err != nil {
					t.Fatal(err)
				}
			}
			got := exec.Session().Snapshot()
			host, ok := sessioncontext.Parse(got[1].Content)
			if !ok || host.Sections.BackgroundMemory != snapshot.Sections.BackgroundMemory || host.Sections.SkillsCatalog != snapshot.Sections.SkillsCatalog {
				t.Fatal("host snapshot lost its personal memory or skill")
			}
			if hidden := host.Sections.Workspace == "" && host.Sections.Environment == ""; hidden != (enabled == "1") {
				t.Fatalf("deployment filtering applied in the wrong mode: origin=%q sections=%+v", got[1].Origin, host.Sections)
			}
			if got[1].ID != old.Snapshot()[1].ID || !reflect.DeepEqual(got[2:], old.Snapshot()[2:]) {
				t.Fatal("refresh changed message identity or non-host content")
			}
			if !reflect.DeepEqual(first.Session().Snapshot().Projection.Messages, before) {
				t.Fatal("filtering rewrote the visible transcript")
			}
		})
	}
}
