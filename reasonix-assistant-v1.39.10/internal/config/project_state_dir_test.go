package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectStateCollisionAssignmentPreservesLegacyDirectory(t *testing.T) {
	state := t.TempDir()
	root := t.TempDir()
	first := filepath.Join(root, "front-end", "app")
	second := filepath.Join(root, "front", "end-app")
	if WorkspaceSlug(first) != WorkspaceSlug(second) {
		t.Fatalf("fixture paths do not collide: %q vs %q", first, second)
	}
	legacy := filepath.Join(state, "projects", WorkspaceSlug(first))
	if got := ProjectStateDir(state, first); got != legacy {
		t.Fatalf("unassigned first project = %q, want %q", got, legacy)
	}
	if got := ProjectStateDir(state, second); got != legacy {
		t.Fatalf("unassigned second project = %q, want %q", got, legacy)
	}
	if _, err := os.Stat(filepath.Join(state, "projects")); !os.IsNotExist(err) {
		t.Fatalf("read-only path resolution created state: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(legacy, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	oldSession := filepath.Join(legacy, "sessions", "existing.jsonl")
	if err := os.WriteFile(oldSession, []byte("existing session\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AssignProjectStateCollision(state, second); err != nil {
		t.Fatal(err)
	}
	if err := AssignProjectStateCollision(state, second); err != nil {
		t.Fatalf("repeat assignment: %v", err)
	}
	newDir := ProjectStateDir(state, second)
	if newDir == legacy || !strings.HasPrefix(filepath.Base(newDir), "@") {
		t.Fatalf("colliding project directory = %q, want distinct assigned path", newDir)
	}
	if got := ProjectStateDir(state, first); got != legacy {
		t.Fatalf("existing project moved to %q from %q", got, legacy)
	}
	if got, err := os.ReadFile(oldSession); err != nil || string(got) != "existing session\n" {
		t.Fatalf("legacy session changed: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(newDir, "sessions", "existing.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("new project sees old session: %v", err)
	}
}

func TestProjectStateNonCollidingRootKeepsHistoricalPath(t *testing.T) {
	state := t.TempDir()
	root := filepath.Join(t.TempDir(), "backend")
	want := filepath.Join(state, "projects", WorkspaceSlug(root))
	if got := ProjectStateDir(state, root); got != want {
		t.Fatalf("non-colliding project = %q, want %q", got, want)
	}
}

func TestMigratedAssistantSessionCatalogUsesStableWorkspaceOnlyForItsOwnProject(t *testing.T) {
	home := t.TempDir()
	oldWorkspace := filepath.Join(t.TempDir(), "old-workspace")
	newWorkspace := filepath.Join(t.TempDir(), "new-workspace")
	otherWorkspace := filepath.Join(t.TempDir(), "other")
	t.Setenv("REASONIX_HOME", home)
	t.Setenv("ASROBOT_INSTANCE_MODE", "isolated")
	t.Setenv("ASROBOT_RUNTIME_WORKSPACE", newWorkspace)
	t.Setenv("REASONIX_CONVERSATION_WORKSPACE_ID", oldWorkspace)
	want := filepath.Join(ProjectStateDir(home, oldWorkspace), "sessions-v4")
	if got := ProjectSessionStoreDir(newWorkspace); got != want {
		t.Fatalf("migrated session catalog = %q, want %q", got, want)
	}
	if got := ProjectSessionStoreDir(otherWorkspace); got != filepath.Join(ProjectStateDir(home, otherWorkspace), "sessions-v4") {
		t.Fatalf("other project catalog changed: %q", got)
	}
	t.Setenv("ASROBOT_INSTANCE_MODE", "")
	if got := ProjectSessionStoreDir(newWorkspace); got != filepath.Join(ProjectStateDir(home, newWorkspace), "sessions-v4") {
		t.Fatalf("legacy path changed: %q", got)
	}
}
