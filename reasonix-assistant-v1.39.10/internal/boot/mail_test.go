package boot

import (
	"context"
	"encoding/json"
	"reasonix/internal/assistant"
	"reasonix/internal/tool"
	"testing"
)

type mailDummyTool string

func (t mailDummyTool) Name() string                                             { return string(t) }
func (t mailDummyTool) Description() string                                      { return "fixture" }
func (t mailDummyTool) Schema() json.RawMessage                                  { return json.RawMessage(`{"type":"object"}`) }
func (t mailDummyTool) ReadOnly() bool                                           { return true }
func (t mailDummyTool) Execute(context.Context, json.RawMessage) (string, error) { return "{}", nil }
func TestMailBackgroundCannotDiscoverTechnicalOrDelegationTools(t *testing.T) {
	t.Setenv("REASONIX_ASSISTANT", "1")
	t.Setenv("REASONIX_ASSISTANT_MAIL_BACKGROUND", "1")
	reg := tool.NewRegistry()
	for _, name := range []string{"assistant_mail", "assistant_history", "assistant_tasks", "assistant_memory", "assistant_mail_schedule", "bash", "read_file", "use_capability", "web_search", "run_skill", "task", "read_only_task"} {
		reg.Add(mailDummyTool(name))
	}
	visible := personalAssistantSurface(reg, nil)
	if len(visible) != 4 || reg.Len() != 4 {
		t.Fatalf("mail background escaped four-tool surface: %v %v", visible, reg.AllNames())
	}
	for _, name := range []string{"bash", "use_capability", "task", "run_skill"} {
		if _, ok := reg.Get(name); ok {
			t.Fatalf("external mail can access %s", name)
		}
	}
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "1")
	for _, tool := range assistant.Tools(t.TempDir(), assistant.Options{}) {
		if _, err := tool.Execute(t.Context(), json.RawMessage(`{"action":"create"}`)); err == nil {
			t.Fatal("background can mutate personal state")
		}
	}
}
