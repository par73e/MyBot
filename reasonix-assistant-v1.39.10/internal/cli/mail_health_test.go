package cli

import (
	"reasonix/internal/assistant"
	"reasonix/internal/heartbeat"
	"testing"
)

func TestLegacyMailHealthDoesNotInvokeAgentOrBrowseMailbox(t *testing.T) {
	r := heartbeatProcessRunner{Executable: "/this-agent-must-not-run"}
	job := heartbeat.Task{ID: "mail-health-fixture", Capability: "mail", Kind: "agent", Prompt: "legacy full mailbox policy", DecideNotification: true}
	result, err := r.Run(t.Context(), job)
	if err != nil || result.Text != assistant.MailHealthMessage || result.SessionID != "" {
		t.Fatal(result, err)
	}
}
