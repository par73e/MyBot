package control

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"reasonix/internal/assistant"
	"reasonix/internal/provider"
	"reasonix/internal/session"
	"reasonix/internal/sessioncontext"
)

// RecordAssistantNotification imports only a delivered, owner-bound notification.
// The gateway holds its normal per-session admission lock. Background reasoning
// and decision envelopes remain in the job transcript, never this conversation.
func (c *Controller) RecordAssistantNotification(ctx context.Context, id, text string, at time.Time) error {
	if !assistant.Enabled() || id == "" || text == "" {
		return errors.New("invalid assistant notification")
	}
	store := c.sessionEventStore()
	if store == nil {
		return session.ErrSessionNotRunning
	}
	message := provider.Message{ID: "notification-" + id, Role: provider.RoleAssistant, Content: text, CreatedAt: at.UnixMilli()}
	payload, err := json.Marshal(map[string]any{"message": message})
	if err != nil {
		return err
	}
	events := []session.Event{{Kind: "message/complete", Payload: payload}}
	c.turnEvents.commitMu.Lock()
	defer c.turnEvents.commitMu.Unlock()
	// Stable operation identity makes a retry after a crash or journal failure
	// return the original commit instead of duplicating the visible message.
	if _, err = c.appendSessionBatch(ctx, store, session.Batch{OperationID: "notification:" + id, Events: events}); err != nil {
		return err
	}
	if _, err = store.Flush(ctx); err != nil {
		return err
	}
	c.noteCommittedMessagesLocked(events)
	c.executor.Session().Replace(store.ExecutionSnapshot().Projection.ModelMessages)
	return nil
}

// AssistantConversationContext is bounded model-reference data for a separate
// background executor. It excludes host snapshots, tools and hidden reasoning.
func (c *Controller) AssistantConversationContext() string {
	if c.executor == nil {
		return ""
	}
	all := c.executor.Session().Snapshot()
	parts := []provider.Message{}
	budget := 8000
	for i := len(all) - 1; i >= 0 && len(parts) < 12 && budget > 0; i-- {
		m := all[i]
		if (m.Role != provider.RoleUser && m.Role != provider.RoleAssistant) || m.Origin == provider.MessageOriginHost || len(m.ToolCalls) > 0 {
			continue
		}
		// Model snapshots can omit Origin; the session-context wrapper remains
		// host-owned and must never masquerade as a user's past statement.
		if _, ok := sessioncontext.Parse(m.Content); ok {
			continue
		}
		body := []rune(m.Content)
		body = body[:min(len(body), min(2000, budget))]
		budget -= len(body)
		parts = append(parts, provider.Message{Role: m.Role, Content: string(body)})
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	raw, _ := json.Marshal(parts)
	return string(raw)
}
