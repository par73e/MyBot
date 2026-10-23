package control

import (
	"context"

	"reasonix/internal/assistant"
	"reasonix/internal/provider"
	"reasonix/internal/sessioncontext"
)

// Reopening an assistant conversation must load the host's current persona.
// Only replace model context; keep the visible transcript and message IDs.
func (c *Controller) refreshPersonalAssistantPrompt() error {
	if !assistant.Enabled() || c.executor == nil {
		return nil
	}
	prompt := c.basePrompt()
	messages := c.executor.Session().Snapshot()
	if len(messages) == 0 {
		return nil
	}
	changed := false
	if prompt != "" && messages[0].Role == provider.RoleSystem && messages[0].Content != prompt {
		messages[0].Content = prompt
		changed = true
	}
	var hostMessages map[string]bool
	filtered := make([]provider.Message, 0, len(messages))
	for _, message := range messages {
		// Older host snapshots may still contain deployment details after an
		// upgrade. Never rewrite the user's own text or assistant replies.
		if message.Role == provider.RoleUser {
			if snapshot, ok := sessioncontext.Parse(message.Content); ok && (snapshot.Sections.Environment != "" || snapshot.Sections.Workspace != "") {
				if message.Origin != provider.MessageOriginHost && hostMessages == nil {
					hostMessages = c.personalAssistantHostMessages()
				}
				if message.Origin != provider.MessageOriginHost && !hostMessages[message.ID] {
					filtered = append(filtered, message)
					continue
				}
				snapshot.Sections.Environment = ""
				snapshot.Sections.Workspace = ""
				message.Content = sessioncontext.Build(snapshot.Sections).Content
				changed = true
				if message.Content == "" {
					continue
				}
			}
		}
		filtered = append(filtered, message)
	}
	if !changed {
		return nil
	}
	return c.replaceSessionModelContext(context.Background(), filtered, "personal-assistant-prompt-refresh")
}

// Execution snapshots omit canonical history and model projections omit Origin.
// Materialize ownership only when migrating a snapshot with deployment fields.
func (c *Controller) personalAssistantHostMessages() map[string]bool {
	ids := make(map[string]bool)
	if store := c.sessionEventStore(); store != nil {
		for _, message := range store.Snapshot().Projection.Messages {
			if message.Role == provider.RoleUser && message.Origin == provider.MessageOriginHost {
				ids[message.ID] = true
			}
		}
	}
	return ids
}
