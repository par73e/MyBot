package bot

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"reasonix/internal/heartbeat"
)

// These endpoints use the existing authenticated loopback control listener.
// Standalone CLI workers use the same owner admission and day routing as the
// embedded worker, rather than opening a second writer over the day transcript.
type assistantControlRequest struct {
	Target         heartbeat.Target `json:"target"`
	At             int64            `json:"at"`
	RunID          string           `json:"run_id,omitempty"`
	Text           string           `json:"text,omitempty"`
	ConversationID string           `json:"conversation_id,omitempty"`
}

func readAssistantControl(w http.ResponseWriter, r *http.Request) (assistantControlRequest, bool) {
	var req assistantControlRequest
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return req, false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || req.At <= 0 {
		http.Error(w, "invalid assistant request", http.StatusBadRequest)
		return req, false
	}
	return req, true
}

func (gw *BotGateway) handleAssistantPrepare(w http.ResponseWriter, r *http.Request) {
	req, ok := readAssistantControl(w, r)
	if !ok {
		return
	}
	out, err := gw.PrepareAssistantTask(r.Context(), req.Target, time.UnixMilli(req.At))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeControlJSON(w, out)
}

func (gw *BotGateway) handleAssistantNotify(w http.ResponseWriter, r *http.Request) {
	req, ok := readAssistantControl(w, r)
	if !ok {
		return
	}
	if req.RunID == "" || req.Text == "" {
		http.Error(w, "run_id and text are required", http.StatusBadRequest)
		return
	}
	err := gw.DeliverAssistantNotification(r.Context(), req.Target, req.RunID, req.Text, req.ConversationID, time.UnixMilli(req.At))
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, heartbeat.ErrDeliveryUncertain) {
			code = http.StatusMultiStatus
		}
		http.Error(w, err.Error(), code)
		return
	}
	writeControlJSON(w, map[string]bool{"delivered": true})
}
