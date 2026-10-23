package bot

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"reasonix/internal/heartbeat"
)

func TestAssistantControlSharesDayAndRequiresAuthentication(t *testing.T) {
	t.Setenv("REASONIX_ASSISTANT", "1")
	t.Setenv("REASONIX_HOME", t.TempDir())
	at := time.Now()
	adapter := newFakeAdapter(PlatformWeixin, "fixture")
	gw, ctrl := notificationGateway(t, adapter, at)
	gw.cfg.ControlToken = "fixture-token"
	body, _ := json.Marshal(assistantControlRequest{Target: heartbeat.Target{ConnectionID: "weixin", ChatID: "owner", ChatType: "dm"}, At: at.UnixMilli(), RunID: "http-run", Text: "http-notice"})
	call := func(path string, handler http.HandlerFunc, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		gw.controlAuth(handler)(w, req)
		gw.turnWG.Wait()
		return w
	}
	if w := call("/assistant/prepare", gw.handleAssistantPrepare, "wrong"); w.Code != http.StatusUnauthorized {
		t.Fatalf("control auth bypassed: %d", w.Code)
	}
	if w := call("/assistant/prepare", gw.handleAssistantPrepare, "fixture-token"); w.Code != http.StatusOK {
		t.Fatalf("prepare failed: %s", w.Body.String())
	}
	for range 2 {
		if w := call("/assistant/notify", gw.handleAssistantNotify, "fixture-token"); w.Code != http.StatusOK {
			t.Fatalf("notify failed: %s", w.Body.String())
		}
	}
	if len(adapter.sentMessages()) != 1 || ctrl.records != 1 {
		t.Fatal("standalone control path duplicated notification")
	}
}
