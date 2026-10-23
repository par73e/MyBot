package bot

import (
	"testing"
	"time"

	"reasonix/internal/sessioninbox"
)

func TestAssistantDailyRoutingAndDurableQueue(t *testing.T) {
	t.Setenv("REASONIX_ASSISTANT", "1")
	msg := InboundMessage{Platform: PlatformWeixin, ConnectionID: "weixin", ChatID: "owner", UserID: "owner", ChatType: ChatDM}
	before, _ := time.Parse(time.RFC3339, "2026-10-23T05:59:59+08:00")
	old := stampAssistantDay(msg, before)
	next := stampAssistantDay(msg, before.Add(time.Second))
	if old.AssistantDay != "2026-10-22" || next.AssistantDay != "2026-10-23" || BuildSessionKey(old.Session()) == BuildSessionKey(next.Session()) {
		t.Fatal("06:00 did not isolate old/new turns")
	}
	gw := NewGateway(GatewayConfig{WorkspaceRoot: "/work"}, nil, nil)
	oldKey, newKey := BuildSessionKey(old.Session()), BuildSessionKey(next.Session())
	if !gw.sessions.TryAcquireIdle(oldKey) || !gw.sessions.TryAcquireIdle(newKey) || gw.sessions.TryAcquireIdle(oldKey) {
		t.Fatal("old/new days did not have independent single-writer admission")
	}
	gw.sessions.Release(oldKey)
	gw.sessions.Release(newKey)
	oldRef := gw.sessionProfileForMessage(old).sessionRef
	newRef := gw.sessionProfileForMessage(next).sessionRef
	if oldRef == newRef || newRef.SessionID != conversationForMessage(next, "/work").SessionID {
		t.Fatal("wrong daily identity")
	}
	restarted := NewGateway(GatewayConfig{WorkspaceRoot: "/work"}, nil, nil)
	if restarted.sessionProfileForMessage(next).sessionRef != newRef {
		t.Fatal("restart changed daily identity")
	}
	queued := botMessageFromEnvelope(sessioninbox.PromptEnvelope{Extra: botInboxExtra(old), SubmitText: "old queued turn"}, next)
	if queued.AssistantDay != old.AssistantDay || BuildSessionKey(queued.Session()) != BuildSessionKey(old.Session()) {
		t.Fatal("queued old-day message moved to new day")
	}
	t.Setenv("REASONIX_ASSISTANT", "")
	if stampAssistantDay(next, before).AssistantDay != "" {
		t.Fatal("daily routing escaped assistant mode")
	}
}
