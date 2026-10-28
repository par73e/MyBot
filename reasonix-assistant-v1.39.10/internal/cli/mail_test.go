package cli

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"reasonix/internal/assistant"
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
	repository := assistant.NewRepository(home)
	if _, err := (assistant.JobStore{Repository: repository}).List(t.Context()); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", repository.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("INSERT INTO assistant_entities(kind,id,payload) VALUES('mail_message',?,?)", task.MailIDs[0], `{"message":{"id":"not-actually-read"},"dedup":"fixture"}`); err != nil {
		t.Fatal(err)
	}
	_, err = (heartbeatProcessRunner{Executable: executable}).Run(t.Context(), task)
	if err == nil || !strings.Contains(err.Error(), "did not successfully read") {
		t.Fatalf("fabricated summary accepted: %v", err)
	}
}
