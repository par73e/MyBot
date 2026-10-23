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

	"reasonix/internal/bot"
	"reasonix/internal/heartbeat"
)

type heartbeatGatewaySender struct {
	gateway heartbeatGateway
	admit   func(context.Context, heartbeat.Task, time.Time) (bool, error)
}

type heartbeatGateway interface {
	SendToAdapter(context.Context, string, string, bot.OutboundMessage) (bot.SendResult, error)
}

func (s heartbeatGatewaySender) Send(ctx context.Context, target heartbeat.Target, text string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := s.gateway.SendToAdapter(ctx, target.ConnectionID, target.Domain, bot.OutboundMessage{
		ConnectionID: target.ConnectionID, Domain: target.Domain, ChatID: target.ChatID,
		ChatType: bot.ChatType(target.ChatType), Text: text,
	})
	if err != nil && (len(result.DeliveredMessageIDs()) > 0 || uncertainHeartbeatSend(err)) {
		return fmt.Errorf("%w: %w", heartbeat.ErrDeliveryUncertain, err)
	}
	return err
}

type heartbeatControlSender struct{}

func (heartbeatControlSender) Send(ctx context.Context, target heartbeat.Target, text string) error {
	cfg, err := loadBotCommandConfig()
	if err != nil {
		return err
	}
	if !cfg.Bot.Control.Enabled {
		return errors.New("enable [bot.control] for standalone heartbeat delivery, or use bot start --heartbeat")
	}
	token := strings.TrimSpace(os.Getenv(cfg.Bot.Control.TokenEnv))
	if token == "" {
		return errors.New("Bot control token is unavailable")
	}
	addr := cfg.Bot.Control.Addr
	if addr == "" {
		addr = "127.0.0.1:37913"
	}
	return heartbeatControlSend(ctx, addr, token, target, text)
}

func heartbeatControlSend(ctx context.Context, addr, token string, target heartbeat.Target, text string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return errors.New("Bot control must use a loopback address")
	}
	body, err := json.Marshal(map[string]string{
		"connection_id": target.ConnectionID, "domain": target.Domain,
		"chat_id": target.ChatID, "chat_type": target.ChatType, "text": text,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/send", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", heartbeat.ErrDeliveryUncertain, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == http.StatusMultiStatus {
		return fmt.Errorf("%w: partial Bot delivery", heartbeat.ErrDeliveryUncertain)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Bot delivery HTTP %d; result retained", resp.StatusCode)
	}
	return nil
}

// Transport failures after request submission can hide a successful send.
// Keep them for inspection rather than automatically creating duplicate messages.
func uncertainHeartbeatSend(err error) bool {
	var network net.Error
	var syntax *json.SyntaxError
	return errors.As(err, &network) || errors.As(err, &syntax) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.Canceled)
}
