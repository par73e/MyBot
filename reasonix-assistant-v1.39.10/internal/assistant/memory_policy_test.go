package assistant

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"reasonix/internal/tool"
)

func TestCalendarReferenceUsesShanghaiAndMondayWeeks(t *testing.T) {
	for _, tc := range []struct{ now, today, nextMonday, nextSaturday string }{
		{"2026-10-24T18:00:00+08:00", "2026-10-24", "2026-10-26", "2026-10-31"},
		{"2026-10-25T23:30:00+08:00", "2026-10-25", "2026-10-26", "2026-10-31"},
		{"2026-10-26T00:30:00+08:00", "2026-10-26", "2026-11-02", "2026-11-07"},
		{"2026-12-31T18:00:00Z", "2027-01-01", "2027-01-04", "2027-01-09"},
	} {
		v, e := time.Parse(time.RFC3339, tc.now)
		if e != nil {
			t.Fatal(e)
		}
		r := calendarReference(v)
		if r["today"] != tc.today || r["next_week_monday"] != tc.nextMonday || r["next_week_saturday"] != tc.nextSaturday {
			t.Fatal(tc, r)
		}
	}
}
func TestForegroundShortMemoryRequiresExplicitApplicabilityStart(t *testing.T) {
	memTool := personalMemoryTool(t.TempDir(), Options{})
	ctx := tool.WithForegroundAuthorization(t.Context(), tool.ForegroundAuthorization{MessageID: "human-plan", Text: "下周在家"})
	_, e := memTool.Execute(ctx, json.RawMessage(`{"action":"create","kind":"short","category":"temporary_plan","source_quote":"下周在家","subject":"临时安排","body":"下周在家","expires_at":"2099-01-01T00:00:00+08:00"}`))
	if e == nil || !strings.Contains(e.Error(), "starts_at") {
		t.Fatal("short memory silently defaulted to now")
	}
	m, e := (Memories{Home: memTool.home}).Apply(t.Context(), MemoryRequest{Action: "list"})
	if e != nil || len(m) != 0 {
		t.Fatal("invalid write was persisted", m, e)
	}
}
