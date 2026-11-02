package assistant

import (
	"testing"
	"time"
)

func TestMemoryViewStateAndSourceMetadata(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	cases := []struct {
		m      Memory
		status string
		source bool
	}{
		{Memory{StartsAt: past}, "active", false},
		{Memory{StartsAt: future, SourceMessageID: "m"}, "future", true},
		{Memory{StartsAt: past, ExpiresAt: &now}, "expired", false},
		{Memory{StartsAt: future, Archived: true}, "archived", false},
	}
	for _, tc := range cases {
		got := makeMemoryView(tc.m, now)
		if got.Status != tc.status || got.SourceAvailable != tc.source {
			t.Fatalf("unexpected view: %+v", got)
		}
	}
}

func TestWebEditClearsOldSourceAttribution(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	m := Memories{Home: home, Now: func() time.Time { return now }}
	created, err := m.Apply(t.Context(), MemoryRequest{Action: "create", Subject: "偏好", Body: "我喜欢简洁", Kind: "long", sourceMessageID: "original"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Apply(t.Context(), MemoryRequest{Action: "update", ID: created[0].ID, Revision: created[0].Revision, Body: "我喜欢详细"})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].SourceMessageID != "" {
		t.Fatal("web edit kept stale source attribution")
	}
	if _, err = m.Apply(t.Context(), MemoryRequest{Action: "archive", ID: created[0].ID, Revision: created[0].Revision}); err == nil {
		t.Fatal("stale revision accepted")
	}
}
