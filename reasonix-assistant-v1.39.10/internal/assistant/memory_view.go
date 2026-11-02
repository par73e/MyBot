package assistant

import "time"

// memoryView augments read responses without changing persisted memory files.
type memoryView struct {
	Memory
	Status          string `json:"status"`
	SourceAvailable bool   `json:"source_metadata_available"`
}

func makeMemoryView(m Memory, now time.Time) memoryView {
	state := "active"
	if m.Archived {
		state = "archived"
	} else if m.ExpiresAt != nil && !now.Before(*m.ExpiresAt) {
		state = "expired"
	} else if now.Before(m.StartsAt) {
		state = "future"
	}
	return memoryView{Memory: m, Status: state, SourceAvailable: m.SourceMessageID != ""}
}

// MemoryViews annotates read-only results, without asserting that source messages remain available.
func MemoryViews(rows []Memory, now time.Time) []memoryView {
	views := make([]memoryView, 0, len(rows))
	for _, m := range rows {
		views = append(views, makeMemoryView(m, now))
	}
	return views
}
