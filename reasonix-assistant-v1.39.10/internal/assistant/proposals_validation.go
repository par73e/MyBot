package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"reasonix/internal/heartbeat"
)

// Shared by preflight and persistence; checking a draft never creates proposals.
func validateTaskCandidates(ctx context.Context, tx *sql.Tx, t heartbeat.Task, d MailDecision) error {
	if len(d.TaskCandidates) > 2 {
		return errors.New("at most two task candidates per mail")
	}
	if len(d.TaskCandidates) == 0 || d.Notify == nil || !*d.Notify {
		return nil
	}
	key := mailHash(t.ID + "\x00" + fmt.Sprint(t.RunStartedAt) + "\x00" + d.ID)
	var raw string
	if e := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='mail_read_receipt' AND id=?", key).Scan(&raw); e != nil || raw != "true" {
		return errors.New("mail task proposals require complete body reads")
	}
	if e := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='mail_proposal_evidence' AND id=?", key).Scan(&raw); e != nil {
		return errors.New("mail task proposals require evidence from the actual read pages")
	}
	var hashes map[string]bool
	if e := json.Unmarshal([]byte(raw), &hashes); e != nil {
		return e
	}
	for i, c := range d.TaskCandidates {
		title := strings.TrimSpace(c.Title)
		if title == "" || utf8.RuneCountInString(title) > 24 || strings.ContainsAny(title, "\r\n") {
			return fmt.Errorf("task_candidates[%d].title must be 1..24 characters without newlines", i)
		}
		if !hashes[mailHash(strings.TrimSpace(c.EvidenceQuote))] {
			return fmt.Errorf("task_candidates[%d].evidence_quote must exactly copy a task_evidence_quote returned by mail read", i)
		}
		if c.DueAt != "" {
			if _, e := timestamp(c.DueAt); e != nil {
				return e
			}
		}
		if c.DueDate != "" {
			if _, e := courseDate(c.DueDate); e != nil {
				return e
			}
		}
		if c.DueAt != "" && c.DueDate != "" {
			return errors.New("candidate date precision must be explicit")
		}
	}
	return nil
}
