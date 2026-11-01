package bot

import (
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

func TestCollectAppendDeduplicatesPlatformRedelivery(t *testing.T) {
	dir := t.TempDir()
	ctrl := control.New(control.Options{
		SessionPath: filepath.Join(dir, "s.jsonl"),
		SessionDir:  dir,
		Sink:        event.Discard,
	})
	t.Cleanup(ctrl.Close)
	first := InboundMessage{
		Platform: PlatformFeishu, ChatType: ChatDM, ChatID: "chat-1",
		UserID: "user-1", MessageID: "msg-1", Text: "first request",
	}
	rec, err := enqueueViaInbox(ctrl, first, sessioninbox.IntentFollowup)
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.MessageID = "msg-2"
	second.Text = "second request"
	for range 2 {
		got, err := collectAppend(ctrl, second, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if got.ItemID != rec.ItemID {
			t.Fatalf("collect item = %q, want %q", got.ItemID, rec.ItemID)
		}
	}
	_, env, err := ctrl.ReadInboxItem(rec.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(env.SubmitText, "second request") != 1 {
		t.Fatalf("redelivered collect body = %q", env.SubmitText)
	}
	recovered := botMessageFromEnvelope(env, InboundMessage{})
	if recovered.Platform != second.Platform || recovered.ChatID != second.ChatID || recovered.MessageID != second.MessageID {
		t.Fatalf("durable route metadata was not updated: %+v", recovered)
	}
}

func TestClaimQueuedInboxAfterActiveTurnFinishes(t *testing.T) {
	dir := t.TempDir()
	ctrl := control.New(control.Options{SessionPath: filepath.Join(dir, "s.jsonl"), SessionDir: dir, Sink: event.Discard})
	t.Cleanup(ctrl.Close)
	gw := NewGateway(GatewayConfig{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	msg := InboundMessage{Platform: PlatformWeixin, ChatType: ChatDM, ChatID: "chat", UserID: "user", MessageID: "second", Text: "second question"}
	key := BuildSessionKey(msg.Session())
	gw.controllers[key] = &sessionState{ctrl: ctrl, sink: &sessionEventSink{}}
	if !gw.sessions.TryAcquireIdle(key) {
		t.Fatal("could not mark first turn active")
	}
	// An empty scan finishes before this durable enqueue completes.
	if got := gw.nextInboxTurn(key, msg); got != nil {
		t.Fatal("inbox should be empty")
	}
	receipt, err := enqueueViaInbox(ctrl, msg, sessioninbox.IntentFollowup)
	if err != nil {
		t.Fatal(err)
	}
	if got := gw.claimQueuedInbox(key, msg); got != nil {
		t.Fatal("must not start a second concurrent turn")
	}
	gw.sessions.Release(key)
	got := gw.claimQueuedInbox(key, msg)
	if got == nil || got.itemID != receipt.ItemID || got.msg.Text != msg.Text {
		t.Fatalf("queued followup = %#v", got)
	}
	if duplicate := gw.claimQueuedInbox(key, msg); duplicate != nil {
		t.Fatal("same item claimed twice")
	}
	gw.sessions.Release(key)
}
