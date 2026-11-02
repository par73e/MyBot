package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"
)

// The path is fixed in the binary. An unavailable collector is an unknown
// result, never a claim that the host or a particular service is healthy.
const serverHealthSocket = "/run/asrobot-health/friday.sock"

type ServerHealthTool struct{}

func (ServerHealthTool) Name() string   { return "assistant_server_health" }
func (ServerHealthTool) ReadOnly() bool { return true }
func (ServerHealthTool) Description() string {
	return "Read the owner's server health snapshot on request. Report disk, memory and load as measured values only: no good/bad, pressure, safety or healthy judgment without configured thresholds. Distinguish service active and HTTP response from actual WeChat delivery. If asrobot_container has reason not_monitored, explain that Docker container status was deliberately not checked because the collector lacks Docker access; it is not an anomaly, failure, stopped container or transient failure, and retrying will not change that. Other unknowns mean information unavailable, not normal or proof of failure. If overall status is unknown do not claim overall healthy or all normal. Never inspect mail or weather."
}
func (ServerHealthTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false,"required":["action"],"properties":{"action":{"type":"string","enum":["status"]}}}`)
}
func (ServerHealthTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var req struct {
		Action string `json:"action"`
	}
	if err := decode(args, &req); err != nil {
		return "", err
	}
	if req.Action != "status" {
		return "", errors.New("invalid server health action")
	}
	return readServerHealth(ctx, serverHealthSocket, time.Now())
}
func readServerHealth(ctx context.Context, path string, now time.Time) (string, error) {
	unknown := `{"status":"unknown","message":"服务器健康状态暂时无法读取"}`
	c, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", path)
	if err != nil {
		return unknown, nil
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err = io.WriteString(c, "status\n"); err != nil {
		return unknown, nil
	}
	raw, err := io.ReadAll(io.LimitReader(c, 32769))
	if err != nil || len(raw) > 32768 {
		return unknown, nil
	}
	var snapshot struct {
		Version     int             `json:"schema_version"`
		CollectedAt time.Time       `json:"collected_at"`
		Items       json.RawMessage `json:"items"`
	}
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.Version != 1 || len(snapshot.Items) == 0 || snapshot.CollectedAt.IsZero() || snapshot.CollectedAt.After(now.Add(time.Minute)) || now.Sub(snapshot.CollectedAt) > time.Minute {
		return unknown, nil
	}
	var items map[string]json.RawMessage
	if json.Unmarshal(snapshot.Items, &items) != nil || len(items) == 0 {
		return unknown, nil
	}
	return string(raw), nil
}
