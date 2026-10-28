package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strconv"
)

// Background mail is a bounded host-owned batch, not a general mailbox browser.
// A manual user query remains unrestricted by this background-only boundary.
func (s MailService) authorizeBackgroundMailQuery(ctx context.Context, r MailRequest) error {
	if os.Getenv("REASONIX_ASSISTANT_MAIL_BACKGROUND") != "1" && os.Getenv("REASONIX_ASSISTANT_MORNING_BACKGROUND") != "1" {
		return nil
	}
	if r.Action == "status" {
		return nil
	}
	id := os.Getenv("REASONIX_ASSISTANT_JOB_ID")
	attempt, err := strconv.ParseInt(os.Getenv("REASONIX_ASSISTANT_MAIL_ATTEMPT"), 10, 64)
	if err != nil || id == "" || attempt <= 0 {
		return errors.New("background mail requires host job and attempt")
	}
	return s.Repository.transact(ctx, func(tx *sql.Tx) error {
		job, err := loadMorningJob(ctx, tx, id)
		if err != nil {
			return err
		}
		if !job.Enabled || len(job.History) == 0 || job.History[len(job.History)-1].StartedAt != attempt || job.History[len(job.History)-1].Status != "running" {
			return errors.New("background mail attempt is no longer running")
		}
		ids := job.MailIDs
		if job.ModuleID == "mail_digest" {
			if r.Action == "silent" || r.Action == "digest_prompt" {
				return nil
			}
			var raw string
			if err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='mail_digest_batch' AND id=?", digestKey(id, attempt)).Scan(&raw); err != nil {
				return err
			}
			var batch digestBatch
			if err := json.Unmarshal([]byte(raw), &batch); err != nil {
				return err
			}
			ids = batch.IDs
		}
		if r.Action != "read" || !containsMailID(ids, r.ID) {
			return errors.New("background mail may only read its host-selected email IDs; list/search/silent cannot expand the batch")
		}
		return nil
	})
}
