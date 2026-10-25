package assistant

import (
	"context"
	"database/sql"
	"reasonix/internal/heartbeat"
	"time"
)

// A failed/expired analysis must not strand cursor-acknowledged mail. Never replay delivery uncertainty.
func releaseFailedMorningMail(ctx context.Context, tx *sql.Tx, records map[string]mailRecord, now time.Time) error {
	jobs, e := loadEntities[heartbeat.Task](ctx, tx, "job")
	if e != nil {
		return e
	}
	failed := map[string]bool{}
	for _, j := range jobs {
		if j.ModuleID != "morning_briefing" || !j.Expired(now) || len(j.History) == 0 {
			continue
		}
		last := j.History[len(j.History)-1]
		if last.Result != "" {
			continue
		}
		switch last.Status {
		case "failed", "interrupted", "deferred", "skipped":
			failed[j.ID] = true
		}
	}
	for id, r := range records {
		if failed[r.JobID] {
			r.JobID = ""
			records[id] = r
		}
	}
	return nil
}
