package cli

import (
	"context"
	"errors"
	"io"
	"net/url"
	"reasonix/internal/bot"
	"reasonix/internal/heartbeat"
	"testing"
)

type uncertainGateway struct{ err error }

func (g uncertainGateway) SendToAdapter(context.Context, string, string, bot.OutboundMessage) (bot.SendResult, error) {
	return bot.SendResult{}, g.err
}
func TestHeartbeatAmbiguousSendIsNotAutomaticallyRetried(t *testing.T) {
	for _, err := range []error{context.DeadlineExceeded, context.Canceled, io.ErrUnexpectedEOF, &url.Error{Op: "Post", URL: "http://fixture", Err: io.EOF}} {
		got := (heartbeatGatewaySender{gateway: uncertainGateway{err}}).Send(t.Context(), heartbeat.Target{ConnectionID: "weixin", ChatID: "owner"}, "test")
		if !errors.Is(got, heartbeat.ErrDeliveryUncertain) {
			t.Fatalf("unsafe retry for %v: %v", err, got)
		}
	}
}
