package control

import (
	"context"
	"reasonix/internal/assistant"
	"reasonix/internal/config"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/sessioncontext"
	"reasonix/internal/skill"
)

// withTurnContext attaches current role-specific snapshots to a host turn.
// Synthetic turns receive bootstrap-only context: they may repair an upgraded
// legacy session with no snapshot, but never advertise a mid-session update.
func (c *Controller) withTurnContext(ctx context.Context, realUserTurn bool) context.Context {
	if c == nil {
		return ctx
	}
	base := c.sessionContextStatic
	if mem := c.memory.current(); mem != nil {
		base.BackgroundMemory = mem.BackgroundDataBlock()
	}

	if assistant.Enabled() {
		base.Environment = ""
		base.Workspace = ""
		snapshot, err := assistant.ContextFor(ctx, config.MemoryUserDir(), time.Now())
		if err != nil {
			snapshot = "\nPersonal assistant state is unavailable. Do not assume any memory or task state, and do not claim a durable change succeeded.\n"
		}
		base.BackgroundMemory += snapshot
	}
	executorSections := base
	plannerSections := base
	if !c.disableImplicitSkillInvocation {
		sk := c.skills.list()
		executorSections.SkillsCatalog = skill.CatalogBlock(sk)
		plannerSections.SkillsCatalog = skill.ReadOnlyCatalogBlock(sk)
	}
	bundle := agent.TurnContextBundle{
		Executor:      sessioncontext.Build(executorSections),
		Planner:       sessioncontext.Build(plannerSections),
		BootstrapOnly: !realUserTurn,
	}
	return agent.WithTurnContextBundle(ctx, bundle)
}
