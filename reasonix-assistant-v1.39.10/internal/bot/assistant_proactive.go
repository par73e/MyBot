package bot

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"reasonix/internal/assistant"
	"reasonix/internal/config"
	"reasonix/internal/heartbeat"
)

// AssistantPreparation contains only host-derived routing and bounded reference
// text. The model can neither choose the owner nor a writable session identity.
type AssistantPreparation struct {
	SessionID string
	Date      string
	Context   string
}

func (gw *BotGateway) assistantTarget(target heartbeat.Target, at time.Time) (AdapterBinding, InboundMessage, error) {
	gw.mu.Lock()
	var binding AdapterBinding
	for _, b := range gw.adapters {
		if b.ID == target.ConnectionID && (target.Domain == "" || b.Domain == target.Domain) {
			binding = b
			break
		}
	}
	gw.mu.Unlock()
	if binding.Adapter == nil || binding.Platform != PlatformWeixin {
		return binding, InboundMessage{}, errors.New("daily assistant requires a connected Weixin target")
	}
	msg := InboundMessage{Platform: binding.Platform, ConnectionID: binding.ID, Domain: binding.Domain, ChatID: target.ChatID, UserID: target.ChatID, ChatType: ChatType(target.ChatType)}
	if msg.ChatType == "" {
		msg.ChatType = ChatDM
	}
	if !gw.checkAllowlist(binding.Platform, msg) {
		return binding, msg, errors.New("assistant target is outside the allowlist")
	}
	msg = stampAssistantDay(msg, at)
	if msg.AssistantDay == "" {
		return binding, msg, errors.New("daily assistant is unavailable for this target")
	}
	return binding, msg, nil
}

// withAssistantConversation shares the ordinary inbound admission lock. It
// cannot race a user turn or steer a notification into an unfinished response.
func (gw *BotGateway) withAssistantConversation(ctx context.Context, target heartbeat.Target, at time.Time, action func(*sessionState, InboundMessage) error) error {
	binding, msg, err := gw.assistantTarget(target, at)
	if err != nil {
		return err
	}
	gw.retireOldAssistantDays(assistant.DailyConversation("", time.Now()).Date)
	key := BuildSessionKey(msg.Session())
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for !gw.sessions.TryAcquireIdle(key) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	defer func() {
		gw.lifecycleMu.Lock()
		rootCtx := gw.runContext
		gw.lifecycleMu.Unlock()
		if rootCtx == nil {
			rootCtx = ctx
		}
		gw.turnWG.Go(func() { gw.finishTurnItem(rootCtx, binding.Adapter, key, msg, nil) })
	}()
	state := gw.getOrCreateSession(ctx, key, msg)
	if state == nil {
		return errors.New("cannot open daily assistant conversation")
	}
	return action(state, msg)
}

func (gw *BotGateway) PrepareAssistantTask(ctx context.Context, target heartbeat.Target, at time.Time) (AssistantPreparation, error) {
	var out AssistantPreparation
	err := gw.withAssistantConversation(ctx, target, at, func(state *sessionState, msg InboundMessage) error {
		reader, ok := state.ctrl.(interface{ AssistantConversationContext() string })
		if !ok {
			return errors.New("assistant conversation reader unavailable")
		}
		out = AssistantPreparation{state.sessionRef.SessionID, msg.AssistantDay, reader.AssistantConversationContext()}
		return nil
	})
	return out, err
}

// DeliverAssistantNotification is synchronous: success means both platform
// delivery and durable conversation import completed. Retries use the outbox.
func (gw *BotGateway) DeliverAssistantNotification(ctx context.Context, target heartbeat.Target, runID, text, conversationID string, at time.Time) error {
	return gw.DeliverAssistantNotificationWhen(ctx, target, runID, text, conversationID, at, nil)
}

// DeliverAssistantNotificationWhen rechecks host policy after acquiring the conversation lock.
func (gw *BotGateway) DeliverAssistantNotificationWhen(ctx context.Context, target heartbeat.Target, runID, text, conversationID string, at time.Time, admit func(context.Context) error) error {
	return gw.withAssistantConversation(ctx, target, at, func(state *sessionState, msg InboundMessage) error {
		c := conversationForMessage(msg, state.workspaceRoot)
		if conversationID != "" && c.SessionID != conversationID {
			return errors.New("notification conversation mismatch")
		}
		repo := assistant.NewRepository(config.MemoryUserDir())
		h := sha256.Sum256([]byte(c.Owner + "\x00" + runID))
		n, err := repo.PrepareNotification(ctx, c.Owner, assistant.Notification{ID: fmt.Sprintf("%x", h[:16]), SessionID: c.SessionID, Text: text, At: at})
		if err != nil {
			return err
		}
		switch n.State {
		case "delivered":
			return nil
		case "sending", "uncertain":
			return heartbeat.ErrDeliveryUncertain
		case "prepared":
			if admit == nil {
				if err := (assistant.HeartbeatService{Home: config.MemoryUserDir()}).CheckRunNotification(ctx, runID, time.Now()); err != nil {
					return err
				}
			}
			if admit != nil {
				if err := admit(ctx); err != nil {
					return err
				}
			}
			if err := repo.NotificationState(ctx, c.Owner, n, "sending"); err != nil {
				return err
			}
			result, sendErr := gw.SendToAdapter(ctx, target.ConnectionID, target.Domain, OutboundMessage{ChatID: target.ChatID, ChatType: msg.ChatType, Text: text})
			if sendErr != nil {
				next := "prepared"
				if len(result.DeliveredMessageIDs()) > 0 || assistantSendUncertain(sendErr) {
					next = "uncertain"
				}
				// If persistence itself fails, the previous "sending" state is
				// deliberately retained and a later worker refuses a blind replay.
				stateErr := repo.NotificationState(context.WithoutCancel(ctx), c.Owner, n, next)
				if next == "uncertain" || stateErr != nil {
					return fmt.Errorf("%w: %w", heartbeat.ErrDeliveryUncertain, sendErr)
				}
				return sendErr
			}
			if err := repo.NotificationState(context.WithoutCancel(ctx), c.Owner, n, "sent"); err != nil {
				return fmt.Errorf("%w: outbox checkpoint failed", heartbeat.ErrDeliveryUncertain)
			}
		case "sent": // delivery succeeded; only repair the transcript below.
		default:
			return errors.New("unknown assistant notification state")
		}
		recorder, ok := state.ctrl.(interface {
			RecordAssistantNotification(context.Context, string, string, time.Time) error
		})
		if !ok {
			return errors.New("assistant notification recorder unavailable")
		}
		if err := recorder.RecordAssistantNotification(ctx, n.ID, n.Text, n.At); err != nil {
			return err
		}
		return repo.NotificationState(ctx, c.Owner, n, "delivered")
	})
}

func assistantSendUncertain(err error) bool {
	var network net.Error
	var syntax *json.SyntaxError
	return errors.As(err, &network) || errors.As(err, &syntax) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.Canceled)
}
