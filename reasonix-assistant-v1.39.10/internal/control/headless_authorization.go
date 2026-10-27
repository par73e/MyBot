package control

import (
	"context"

	"reasonix/internal/agent"
	"reasonix/internal/tool"
)

// Run is a directly submitted headless user turn. Bind its raw input and the
// same durable message identity as the interactive turn path, before composing
// references or model context. Synthetic continuations use the orchestrator's
// separate host-origin path; background assistant writes remain prohibited.
func (c *Controller) headlessForegroundContext(ctx context.Context, input string) context.Context {
	id := agent.NewMessageID()
	if c.executor != nil {
		ctx = agent.WithUserMessageIdentity(ctx, c.executor.Session(), id)
	}
	return tool.WithForegroundAuthorization(ctx, c.foregroundAuthorization(orchestratedTurn{raw: input}, id))
}
