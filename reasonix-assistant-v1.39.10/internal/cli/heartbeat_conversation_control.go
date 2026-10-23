package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"reasonix/internal/assistant"
	"reasonix/internal/bot"
	"reasonix/internal/heartbeat"
)

type heartbeatControlConversation struct{}

func (heartbeatControlConversation) SendToAdapter(ctx context.Context, connection, domain string, msg bot.OutboundMessage) (bot.SendResult, error) {
	err := (heartbeatControlSender{}).Send(ctx, heartbeat.Target{ConnectionID: connection, Domain: domain, ChatID: msg.ChatID, ChatType: string(msg.ChatType)}, msg.Text)
	return bot.SendResult{}, err
}
func (heartbeatControlConversation) PrepareAssistantTask(ctx context.Context, target heartbeat.Target, at time.Time) (bot.AssistantPreparation, error) {
	var out bot.AssistantPreparation
	err := assistantControlCall(ctx, "/assistant/prepare", map[string]any{"target": target, "at": at.UnixMilli()}, &out)
	return out, err
}
func (heartbeatControlConversation) DeliverAssistantNotification(ctx context.Context, target heartbeat.Target, id, text, conversationID string, at time.Time) error {
	err := assistantControlCall(ctx, "/assistant/notify", map[string]any{"target": target, "at": at.UnixMilli(), "run_id": id, "text": text, "conversation_id": conversationID}, nil)
	if err != nil && uncertainHeartbeatSend(err) {
		return fmt.Errorf("%w: %w", heartbeat.ErrDeliveryUncertain, err)
	}
	return err
}
func (heartbeatControlSender) SendRun(ctx context.Context, t heartbeat.Task, run heartbeat.Run) error {
	if t.Target == nil {
		return errors.New("notification target missing")
	}
	if !assistant.Enabled() {
		return (heartbeatControlSender{}).Send(ctx, *t.Target, run.Result)
	}
	return (heartbeatControlConversation{}).DeliverAssistantNotification(ctx, *t.Target, fmt.Sprintf("%s:%d", t.ID, run.StartedAt), run.Result, run.ConversationID, time.UnixMilli(run.StartedAt))
}

func assistantControlCall(ctx context.Context, path string, in, out any) error {
	cfg, err := loadBotCommandConfig()
	if err != nil {
		return err
	}
	if !cfg.Bot.Control.Enabled {
		return errors.New("standalone assistant tasks require the authenticated Bot control listener; use bot start --heartbeat instead")
	}
	token := strings.TrimSpace(os.Getenv(cfg.Bot.Control.TokenEnv))
	if token == "" {
		return errors.New("Bot control token is unavailable")
	}
	addr := cfg.Bot.Control.Addr
	if addr == "" {
		addr = "127.0.0.1:37913"
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return errors.New("Bot control must use a loopback address")
	}
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusMultiStatus {
		return heartbeat.ErrDeliveryUncertain
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("assistant Bot control HTTP %d", resp.StatusCode)
	}
	if out == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return err
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}
