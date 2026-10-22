package control

import (
	"context"

	"reasonix/internal/assistant"
	"reasonix/internal/provider"
)

// Reopening an assistant conversation must load the host's current persona.
// Only replace model context; keep the visible transcript and message IDs.
func (c *Controller) refreshPersonalAssistantPrompt() error {
	if !assistant.Enabled() || c.executor == nil {
		return nil
	}
	prompt := c.basePrompt()
	messages := c.executor.Session().Snapshot()
	if prompt == "" || len(messages) == 0 || messages[0].Role != provider.RoleSystem || messages[0].Content == prompt {
		return nil
	}
	messages[0].Content = prompt
	return c.replaceSessionModelContext(context.Background(), messages, "personal-assistant-prompt-refresh")
}
