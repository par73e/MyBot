package cli

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestHeartbeatFailedProcessPreservesStructuredDiagnosticAndSession(t *testing.T) {
	runner, job, _ := morningRunnerFixture(t, 5*time.Minute)
	writeMorningAgent(t, runner, `printf '%s\n' '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"provider stream interrupted","session_id":"fixture-session","error_code":"stream_failed"}'
exit 1
`)
	result, err := runner.runAgent(t.Context(), job)
	if err == nil || result.SessionID != "fixture-session" || !strings.Contains(err.Error(), "provider stream interrupted") || !strings.Contains(err.Error(), "stream_failed") || !strings.Contains(err.Error(), "error_during_execution") {
		t.Fatal(result, err)
	}
}

func TestHeartbeatFailureDiagnosticBoundsAndOmitsStructuredDraft(t *testing.T) {
	result := heartbeatAgentResult{IsError: true, Result: `{"mail_decisions":["PRIVATE_MAIL_MARKER"]}`}
	err := heartbeatAgentFailure(errors.New("exit status 1"), result, "")
	if strings.Contains(err.Error(), "PRIVATE_MAIL_MARKER") || !strings.Contains(err.Error(), "interrupted structured result") {
		t.Fatal(err)
	}
	result.Result = strings.Repeat("长", 3000)
	err = heartbeatAgentFailure(errors.New("exit status 1"), result, "")
	if !strings.Contains(err.Error(), "[truncated]") || len([]rune(err.Error())) > 2200 {
		t.Fatal("unbounded diagnostic", len([]rune(err.Error())))
	}
}
