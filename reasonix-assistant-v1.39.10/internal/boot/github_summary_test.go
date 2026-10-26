package boot

import (
	"testing"

	"reasonix/internal/tool"
)

func TestGitHubSummaryCannotDiscoverMutationOrNetworkTools(t *testing.T) {
	t.Setenv("REASONIX_ASSISTANT", "1")
	t.Setenv("REASONIX_ASSISTANT_GITHUB_BACKGROUND", "1")
	reg := tool.NewRegistry()
	for _, name := range []string{"assistant_github_notification", "assistant_github", "assistant_tasks", "assistant_memory", "bash", "use_capability", "web_search", "task"} {
		reg.Add(mailDummyTool(name))
	}
	visible := personalAssistantSurface(reg, nil)
	if len(visible) != 1 || visible[0] != "assistant_github_notification" || reg.Len() != 1 {
		t.Fatal("GitHub summary escaped read-only snapshot scope", visible, reg.AllNames())
	}
}
