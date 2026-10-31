package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reasonix/internal/provider"
	"reasonix/internal/session"
	"reasonix/internal/sessioncontext"
	"strings"
	"testing"
)

type fakeHistory struct{ seen []string }

func (f *fakeHistory) History(ctx context.Context, ref session.SessionRef) ([]provider.Message, error) {
	f.seen = append(f.seen, ref.SessionID)
	out := []provider.Message{}
	for i := 0; i < 7; i++ {
		out = append(out, provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("item %d pasted-key", i), CreatedAt: 0})
	}
	return out, nil
}
func fixture(t *testing.T) *bridge {
	t.Helper()
	root := t.TempDir()
	for _, sid := range []string{"friday-mine-20300101", "friday-other-20300101", "friday-mine-20300101-task", "friday-mine-20301399"} {
		dir := filepath.Join(root, sid)
		if e := os.Mkdir(dir, 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{}"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	return &bridge{query: &fakeHistory{}, root: root, owner: "mine", legacy: "old", token: strings.Repeat("a", 48), redact: []string{"pasted-key"}, gate: make(chan struct{}, 1)}
}
func TestVisibleOnlyBusinessConversation(t *testing.T) {
	hidden := []provider.Message{
		{Role: provider.RoleSystem, Content: "system"},
		{Role: provider.RoleTool, Content: "tool"},
		{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: "host"},
		{Role: provider.RoleAssistant, Content: "partial", LocalOnly: true},
		{Role: provider.RoleAssistant, Content: "tool preamble", ToolCalls: []provider.ToolCall{{}}},
		{Role: provider.RoleUser, Content: sessioncontext.Build(sessioncontext.Sections{Workspace: "context"}).Content},
	}
	for _, m := range hidden {
		if visible(m) {
			t.Fatal("internal record exposed")
		}
	}
	m := provider.Message{Role: provider.RoleAssistant, Content: "visible", ReasoningContent: "hidden reasoning", RawContent: "hidden raw"}
	if !visible(m) {
		t.Fatal("answer hidden")
	}
}
func TestPaginationOwnershipAndUnknownTimestamps(t *testing.T) {
	b := fixture(t)
	cursor := ""
	seen := map[string]bool{}
	for n := 0; n < 4; n++ {
		page, e := b.page(context.Background(), "2030-01-01", "", cursor, 2)
		if e != nil {
			t.Fatal(e)
		}
		p := page.(map[string]any)
		for _, m := range p["messages"].([]visibleMessage) {
			if seen[m.ID] {
				t.Fatal("duplicate cursor item")
			}
			seen[m.ID] = true
			if m.Created != 0 {
				t.Fatal("invented timestamp")
			}
			if strings.Contains(m.Text, "pasted-key") {
				t.Fatal("credential exposed")
			}
		}
		cursor = p["next_cursor"].(string)
		if !p["has_more"].(bool) {
			break
		}
	}
	if len(seen) != 7 {
		t.Fatalf("lost messages: %d", len(seen))
	}
	for _, sid := range b.query.(*fakeHistory).seen {
		if sid != "friday-mine-20300101" {
			t.Fatal("cross-owner session read")
		}
	}
}
func TestCursorCannotChangeSearchOrBeForged(t *testing.T) {
	b := fixture(t)
	raw := b.encode(position{Day: "2030-01-01", Session: "owned", Ordinal: 4, Scope: b.scope("2030-01-01", "")})
	if _, e := b.decode(raw, b.scope("2030-01-01", "new search")); e == nil {
		t.Fatal("cursor reused for different scope")
	}
	if _, e := b.decode(raw+"X", b.scope("2030-01-01", "")); e == nil {
		t.Fatal("forged cursor accepted")
	}
}
func TestHTTPRejectsUnauthenticatedAndForeignSource(t *testing.T) {
	b := fixture(t)
	for _, tc := range []struct {
		url, auth string
		status    int
	}{
		{"/v1/days?source=friday_owner", "", 403},
		{"/v1/days?source=other", "Bearer " + b.token, 404},
		{"/v1/messages?source=friday_owner&date=../../secret", "Bearer " + b.token, 400},
		{"/v1/messages?source=friday_owner&cursor=forged", "Bearer " + b.token, 400},
	} {
		r := httptest.NewRequest("GET", tc.url, nil)
		r.Header.Set("Authorization", tc.auth)
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("status %d", w.Code)
		}
	}
}
func TestSerializedMessageOmitsReasoningRawAndToolFields(t *testing.T) {
	raw, _ := json.Marshal(visibleMessage{Role: "assistant", Text: "answer"})
	for _, key := range []string{"reasoning", "raw_content", "tool_calls"} {
		if strings.Contains(string(raw), key) {
			t.Fatal("internal field exposed")
		}
	}
}
