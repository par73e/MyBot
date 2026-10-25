package control

import (
	"context"
	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
	"testing"
)

type authorizationRunner struct {
	proof   tool.ForegroundAuthorization
	present bool
}

func (r *authorizationRunner) Run(ctx context.Context, _ string) error {
	r.proof, r.present = tool.ForegroundAuthorizationFrom(ctx)
	return nil
}
func TestForegroundProposalAuthorizationUsesActualTurnOrigin(t *testing.T) {
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "要我记下报名材料吗？"})
	exec := agent.New(&scriptedTurns{}, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	runner := &authorizationRunner{}
	c := newOwnedTestController(t, Options{Runner: runner, Executor: exec, Label: "test"})
	if e := newTurnOrchestrator(c).runTurnWithRawDisplay(t.Context(), "composed wrapper", "可以，帮我记下", ""); e != nil {
		t.Fatal(e)
	}
	if !runner.present || runner.proof.Text != "可以，帮我记下" || runner.proof.MessageID == "" || runner.proof.PreviousAssistant != "要我记下报名材料吗？" {
		t.Fatal(runner.proof, runner.present)
	}
	if e := newTurnOrchestrator(c).runComposedSyntheticTurn(t.Context(), "可以，帮我记下"); e != nil {
		t.Fatal(e)
	}
	if runner.present {
		t.Fatal("synthetic continuation carried human approval", runner.proof)
	}
}
