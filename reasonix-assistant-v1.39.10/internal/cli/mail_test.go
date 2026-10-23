package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"reasonix/internal/heartbeat"
)

func TestMailRunnerRejectsFinalDecisionWithoutActualBodyReads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture shell is Unix only")
	}
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	t.Setenv("HOME", home)
	executable := filepath.Join(home, "fake-agent")
	script := `#!/bin/sh
printf '%s\n' '{"type":"result","result":"{\"notify\":true,\"message\":\"fabricated\",\"reason\":\"claimed to read\"}"}'
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	task := heartbeat.Task{ID: "mail-analyze-fixture", Capability: "mail", MailIDs: []string{"not-actually-read"}, RunStartedAt: 123, DecideNotification: true, WorkspaceRoot: home, ApprovalMode: "read-only"}
	_, err := (heartbeatProcessRunner{Executable: executable}).Run(t.Context(), task)
	if err == nil || !strings.Contains(err.Error(), "did not successfully read") {
		t.Fatalf("fabricated summary accepted: %v", err)
	}
}
