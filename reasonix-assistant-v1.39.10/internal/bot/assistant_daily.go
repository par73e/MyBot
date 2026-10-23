package bot

import (
	"context"
	"fmt"
	"reasonix/internal/control"
	"reasonix/internal/session"
	"strings"
	"time"

	"reasonix/internal/assistant"
	"reasonix/internal/config"
)

func stampAssistantDay(msg InboundMessage, now time.Time) InboundMessage {
	msg.AssistantDay = ""
	if assistant.Enabled() && msg.Platform == PlatformWeixin && (msg.ChatType == ChatDM || msg.ChatType == ChatDirect) {
		msg.AssistantDay = assistant.DailyConversation("", now).Date
	}
	return msg
}

func conversationForMessage(msg InboundMessage, workspace string) assistant.Conversation {
	owner := assistant.ConversationOwner(msg.ConnectionID, msg.ChatID, workspace)
	day, _ := time.ParseInLocation(time.DateOnly, msg.AssistantDay, mustAssistantLocation())
	return assistant.DailyConversation(owner, day.Add(6*time.Hour))
}

func mustAssistantLocation() *time.Location {
	loc, _ := time.LoadLocation(assistant.DailyTimezone)
	return loc
}

func registerDailyIdentity(ctx context.Context, msg InboundMessage, profile sessionRuntimeProfile, identity control.IdentityLifecycle, ref session.SessionRef) error {
	if msg.AssistantDay == "" {
		return nil
	}
	c := conversationForMessage(msg, profile.workspaceRoot)
	if ref.SessionID != c.SessionID {
		return fmt.Errorf("daily conversation identity mismatch")
	}
	titler, ok := identity.(interface {
		SetSessionTitle(context.Context, string) error
		Snapshot() error
	})
	if !ok {
		return fmt.Errorf("daily conversation requires native title support")
	}
	if err := titler.SetSessionTitle(ctx, c.Title); err != nil {
		return err
	}
	// Snapshot is the durable semantic checkpoint for the title, without sampling.
	if err := titler.Snapshot(); err != nil {
		return err
	}
	return assistant.NewRepository(config.MemoryUserDir()).EnsureConversation(ctx, c)
}

// Release idle old controllers instead of retaining a month's model contexts in
// RAM. Active old turns keep their identity and finish normally across 06:00.
func (gw *BotGateway) retireOldAssistantDays(today string) {
	if today == "" {
		return
	}
	gw.mu.Lock()
	keys := []string{}
	for key, state := range gw.controllers {
		if assistantStateDay(state) != "" && assistantStateDay(state) < today {
			keys = append(keys, key)
		}
	}
	gw.mu.Unlock()
	for _, key := range keys {
		if !gw.sessions.TryAcquireIdle(key) {
			continue
		}
		gw.mu.Lock()
		state := gw.controllers[key]
		if state != nil && !botSessionHasActiveWork(state) {
			delete(gw.controllers, key)
			delete(gw.sessionOverrides, key)
		} else {
			state = nil
		}
		gw.mu.Unlock()
		gw.closeSessionState(state)
		gw.sessions.Release(key)
	}
}

func assistantStateDay(state *sessionState) string {
	id := state.sessionRef.SessionID
	if !strings.HasPrefix(id, "friday-") || len(id) < 8 {
		return ""
	}
	day, err := time.Parse("20060102", id[len(id)-8:])
	if err != nil {
		return ""
	}
	return day.Format(time.DateOnly)
}
func canDegradeBotMapping(msg InboundMessage, profile sessionRuntimeProfile) bool {
	return msg.AssistantDay == "" && (profile.sessionRefOptional || profile.sessionPathOptional)
}
func (gw *BotGateway) handleDailySessionCommand(ctx context.Context, adapter Adapter, msg InboundMessage) bool {
	if msg.AssistantDay == "" {
		return false
	}
	fields := strings.Fields(msg.Text)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "/new", "/reset", "/attach":
		_ = gw.sendText(ctx, adapter, msg, "Sir，日常会话每天北京时间 06:00 自动切换；回顾过去的聊天可以直接告诉我日期或话题。")
		return true
	default:
		return false
	}
}
