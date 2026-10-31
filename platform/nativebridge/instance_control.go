package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"time"
)

type instanceControlResponse struct {
	OK             bool `json:"ok"`
	Running        bool `json:"running"`
	DesiredRunning bool `json:"desired_running"`
}

func (b *bridge) instanceControl(ctx context.Context, action string) (instanceControlResponse, error) {
	var response instanceControlResponse
	if action != "status" && action != "start" && action != "stop" && action != "restart" {
		return response, errors.New("invalid instance action")
	}
	conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", filepath.Join(state, "control.sock"))
	if err != nil {
		return response, err
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return response, err
	}
	if err = json.NewEncoder(conn).Encode(map[string]string{"token": b.token, "action": action}); err != nil {
		return response, err
	}
	reader := bufio.NewReaderSize(conn, 4096)
	raw, err := reader.ReadSlice('\n')
	if err != nil {
		return response, err
	}
	if err = json.Unmarshal(raw, &response); err != nil || !response.OK {
		return response, errors.New("instance control failed")
	}
	return response, nil
}
