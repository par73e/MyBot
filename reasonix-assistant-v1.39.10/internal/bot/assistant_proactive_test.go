package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/heartbeat"
	"reasonix/internal/session"
)

type notificationController struct {
	stubBotController
	ref     session.SessionRef
	fail    bool
	records int
}

func (c *notificationController) RecordAssistantNotification(context.Context, string, string, time.Time) error {
	c.records++
	if c.fail {
		return errors.New("fixture transcript checkpoint failure")
	}
	return nil
}
func (c *notificationController) SessionRef() (session.SessionRef, bool) { return c.ref, true }
func (*notificationController) SessionService() *session.Service         { return nil }
func (*notificationController) UsesExclusiveSession() bool               { return true }
func (*notificationController) BindFreshSession(context.Context, string) (session.SessionRef, error) {
	return session.SessionRef{}, errors.New("unexpected bind")
}
func (*notificationController) OpenSession(context.Context, session.SessionRef) (session.SessionRef, error) {
	return session.SessionRef{}, errors.New("unexpected open")
}
func (*notificationController) ContinueLegacySession(context.Context, string, string) (session.SessionRef, error) {
	return session.SessionRef{}, errors.New("unexpected legacy")
}
func (*notificationController) ContinuePrototypeSession(context.Context, string) (session.SessionRef, error) {
	return session.SessionRef{}, errors.New("unexpected prototype")
}

func (*notificationController) AssistantConversationContext() string { return "[]" }
func (*notificationController) RuntimeStatus() control.RuntimeStatus { return control.RuntimeStatus{} }

func notificationGateway(t *testing.T, adapter Adapter, at time.Time) (*BotGateway, *notificationController) {
	t.Helper()
	gw := NewGatewayWithAdapterBindings(GatewayConfig{WorkspaceRoot: "/work", Allowlist: AllowlistConfig{Enabled: true, Users: map[Platform][]string{PlatformWeixin: {"owner"}}}}, []AdapterBinding{{ID: "weixin", Domain: "weixin", Platform: PlatformWeixin, Adapter: adapter}}, nil)
	msg := stampAssistantDay(InboundMessage{Platform: PlatformWeixin, ConnectionID: "weixin", Domain: "weixin", ChatID: "owner", UserID: "owner", ChatType: ChatDM}, at)
	c := conversationForMessage(msg, "/work")
	ctrl := &notificationController{ref: session.SessionRef{SessionID: c.SessionID}}
	gw.controllers[BuildSessionKey(msg.Session())] = &sessionState{ctrl: ctrl, sessionRef: session.SessionRef{SessionID: c.SessionID}, workspaceRoot: "/work", toolApprovalMode: gw.sessionProfileForMessage(msg).toolApprovalMode}
	t.Cleanup(func() { gw.turnWG.Wait() })
	return gw, ctrl
}

func TestAssistantOutboxRetriesOnlyMissingTranscriptAndSurvivesRestart(t *testing.T) {
	t.Setenv("REASONIX_ASSISTANT", "1")
	t.Setenv("REASONIX_HOME", t.TempDir())
	adapter := newFakeAdapter(PlatformWeixin, "fixture")
	at := time.Now()
	gw, ctrl := notificationGateway(t, adapter, at)
	target := heartbeat.Target{ConnectionID: "weixin", Domain: "weixin", ChatID: "owner", ChatType: "dm"}
	ctrl.fail = true
	if err := gw.DeliverAssistantNotification(t.Context(), target, "run-1", "report", "", at); err == nil {
		t.Fatal("checkpoint failure hidden")
	}
	gw.turnWG.Wait()
	ctrl.fail = false
	for range 2 {
		if err := gw.DeliverAssistantNotification(t.Context(), target, "run-1", "report", "", at); err != nil {
			t.Fatal(err)
		}
		gw.turnWG.Wait()
	}
	if len(adapter.sentMessages()) != 1 || ctrl.records != 2 {
		t.Fatalf("sent=%d records=%d; retry must only repair history", len(adapter.sentMessages()), ctrl.records)
	}
	restarted, other := notificationGateway(t, adapter, at)
	if err := restarted.DeliverAssistantNotification(t.Context(), target, "run-1", "report", "", at); err != nil {
		t.Fatal(err)
	}
	restarted.turnWG.Wait()
	if len(adapter.sentMessages()) != 1 || other.records != 0 {
		t.Fatal("restart duplicated delivery or transcript")
	}
}

func TestAssistantOutboxRefusesAmbiguousResend(t *testing.T) {
	t.Setenv("REASONIX_ASSISTANT", "1")
	t.Setenv("REASONIX_HOME", t.TempDir())
	adapter := &resultAdapter{fakeAdapter: newFakeAdapter(PlatformWeixin, "fixture"), err: context.DeadlineExceeded}
	at := time.Now()
	gw, ctrl := notificationGateway(t, adapter, at)
	target := heartbeat.Target{ConnectionID: "weixin", Domain: "weixin", ChatID: "owner", ChatType: "dm"}
	for range 2 {
		if err := gw.DeliverAssistantNotification(t.Context(), target, "uncertain", "report", "", at); !errors.Is(err, heartbeat.ErrDeliveryUncertain) {
			t.Fatalf("unsafe retry status: %v", err)
		}
		gw.turnWG.Wait()
	}
	if len(adapter.sentMessages()) != 1 || ctrl.records != 0 {
		t.Fatal("ambiguous delivery was replayed or claimed delivered in history")
	}
}

func TestAssistantProactiveWaitsForUserTurnAndRejectsForeignTarget(t *testing.T) {
	t.Setenv("REASONIX_ASSISTANT", "1")
	t.Setenv("REASONIX_HOME", t.TempDir())
	at := time.Now()
	gw, _ := notificationGateway(t, newFakeAdapter(PlatformWeixin, "fixture"), at)
	target := heartbeat.Target{ConnectionID: "weixin", ChatID: "owner", ChatType: "dm"}
	_, msg, err := gw.assistantTarget(target, at)
	if err != nil {
		t.Fatal(err)
	}
	key := BuildSessionKey(msg.Session())
	gw.sessions.TryAcquireIdle(key)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := gw.PrepareAssistantTask(ctx, target, at); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("background bypassed active user turn: %v", err)
	}
	gw.sessions.Release(key)
	target.ChatID = "other"
	if _, err := gw.PrepareAssistantTask(t.Context(), target, at); err == nil {
		t.Fatal("foreign notification target admitted")
	}
}

func TestMailDeliveryAdmissionRunsInsideConversationBeforeSend(t *testing.T) {
	t.Setenv("REASONIX_ASSISTANT", "1")
	t.Setenv("REASONIX_HOME", t.TempDir())
	adapter := newFakeAdapter(PlatformWeixin, "fixture")
	at := time.Now()
	gw, ctrl := notificationGateway(t, adapter, at)
	target := heartbeat.Target{ConnectionID: "weixin", Domain: "weixin", ChatID: "owner", ChatType: "dm"}
	called := false
	err := gw.DeliverAssistantNotificationWhen(t.Context(), target, "paused-mail", "notice", "", at, func(context.Context) error { called = true; return heartbeat.ErrDeferred })
	if !called || !errors.Is(err, heartbeat.ErrDeferred) || ctrl.records != 0 {
		t.Fatal("closed policy was not enforced", called, err, ctrl.records)
	}
	if err = gw.DeliverAssistantNotificationWhen(t.Context(), target, "paused-mail", "notice", "", at, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if ctrl.records != 1 {
		t.Fatal("resumed notification was not imported once", ctrl.records)
	}
}
