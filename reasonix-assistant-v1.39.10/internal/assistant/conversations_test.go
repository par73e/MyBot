package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/session"
	"reasonix/internal/sessioncontext"
)

func TestDailyConversationBoundaryAndRestartIdentity(t *testing.T) {
	owner := ConversationOwner("weixin", "owner", "/work")
	for _, tt := range []struct{ at, date string }{
		{"2026-10-23T05:59:59+08:00", "2026-10-22"},
		{"2026-10-23T06:00:00+08:00", "2026-10-23"},
		{"2026-10-23T21:59:59Z", "2026-10-23"},
		{"2026-10-23T22:00:00Z", "2026-10-24"},
	} {
		at, _ := time.Parse(time.RFC3339, tt.at)
		c := DailyConversation(owner, at)
		if c.Date != tt.date || c.Title != tt.date || c.EndsAt.Sub(c.StartsAt) != 24*time.Hour {
			t.Fatalf("wrong assistant day: %+v", c)
		}
	}
	at, _ := time.Parse(time.RFC3339, "2026-10-23T07:00:00+08:00")
	c := DailyConversation(owner, at)
	repo := NewRepository(t.TempDir())
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := repo.EnsureConversation(context.Background(), c); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	rows, err := (Repository{Path: repo.Path}).Conversations(t.Context(), owner, "", "", "", 20)
	if err != nil || len(rows) != 1 || rows[0].SessionID != DailyConversation(owner, at.Add(12*time.Hour)).SessionID {
		t.Fatalf("restart/concurrent first trigger lost identity: %+v %v", rows, err)
	}
	foreign, _ := repo.Conversations(t.Context(), ConversationOwner("weixin", "other", "/work"), "", "", "", 20)
	if len(foreign) != 0 {
		t.Fatal("daily history crossed owner boundary")
	}
}

func TestDailyHistoryNativeReadSearchAndOwnerIsolation(t *testing.T) {
	repo := NewRepository(t.TempDir())
	owner := ConversationOwner("weixin", "owner", "/work")
	at, _ := time.Parse(time.RFC3339, "2026-10-23T07:00:00+08:00")
	c := DailyConversation(owner, at)
	if err := repo.EnsureConversation(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	service, err := session.NewService("local", session.NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: c.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	host := sessioncontext.Build(sessioncontext.Sections{BackgroundMemory: "JapanTrip HOST_SECRET"}).Content
	for _, m := range []provider.Message{
		{ID: "system", Role: provider.RoleSystem, Content: "JapanTrip SYSTEM_SECRET"},
		{ID: "host", Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: host},
		{ID: "user", Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "JapanTrip 日本旅行计划", CreatedAt: at.UnixMilli()},
		{ID: "answer", Role: provider.RoleAssistant, Content: "JapanTrip 先去东京", CreatedAt: at.Add(time.Second).UnixMilli()},
		{ID: "tool", Role: provider.RoleTool, Content: "JapanTrip TOOL_SECRET"},
		{ID: "long", Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: strings.Repeat("长", 4500) + "TAIL", CreatedAt: at.UnixMilli()},
	} {
		payload, _ := json.Marshal(map[string]any{"message": m})
		if _, err := runtime.Session().AppendBatch(t.Context(), m.ID, []session.Event{{Kind: "message/complete", Payload: payload}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	tool := HistoryTool{Repository: repo, Owner: owner, Query: service.Query()}
	call := func(args string) string {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			out, err := tool.Execute(t.Context(), json.RawMessage(args))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, `"status":"preparing"`) {
				return out
			}
			if time.Now().After(deadline) {
				t.Fatal("native history did not finish preparation")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	for _, args := range []string{`{"action":"read","date":"2026-10-23"}`, `{"action":"search","date":"2026-10-23","query":"JapanTrip"}`} {
		out := call(args)
		if !strings.Contains(out, "JapanTrip") || strings.Contains(out, "HOST_SECRET") || strings.Contains(out, "SYSTEM_SECRET") || strings.Contains(out, "TOOL_SECRET") {
			t.Fatalf("wrong native history: %s", out)
		}
	}
	out := call(`{"action":"read","date":"2026-10-23","message_id":"long","text_offset":4000}`)
	if !strings.Contains(out, "TAIL") {
		t.Fatal("long message continuation lost its tail")
	}
	legacyID := LegacyConversationID("weixin", "owner")
	legacy, err := service.Create(t.Context(), session.CreateOptions{SessionID: legacyID})
	if err != nil {
		t.Fatal(err)
	}
	early := at.Add(-5 * time.Hour)
	payload, _ := json.Marshal(map[string]any{"message": provider.Message{ID: "overnight", Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "OVERNIGHT_LEGACY", CreatedAt: early.UnixMilli()}})
	if _, err := legacy.Session().AppendBatch(t.Context(), "overnight", []session.Event{{Kind: "message/complete", Payload: payload}}); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	tool.LegacySessionID = legacyID
	if out := call(`{"action":"read","calendar_date":"2026-10-23"}`); !strings.Contains(out, "OVERNIGHT_LEGACY") {
		t.Fatal("natural-day lookup omitted overnight legacy conversation")
	}
	if out := call(`{"action":"read","date":"2026-10-23"}`); strings.Contains(out, "OVERNIGHT_LEGACY") {
		t.Fatal("06:00 day lookup included 02:00 legacy content")
	}
	tool.Owner = "other-owner"
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"action":"read","session_id":"`+c.SessionID+`"}`)); err == nil {
		t.Fatal("foreign session accepted")
	}
}
