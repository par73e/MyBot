package assistant

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"reasonix/internal/heartbeat"
)

// This is a native administration operation, deliberately absent from model
// tools. An operator must identify the exact body reads and visible anchors.
type MailDeliveryRepair struct {
	JobID   string                   `json:"job_id"`
	Attempt int64                    `json:"attempt"`
	Apply   bool                     `json:"apply"`
	Items   []MailDeliveryRepairItem `json:"items"`
}
type MailDeliveryRepairItem struct {
	ID     string `json:"id"`
	Anchor string `json:"anchor"`
}

func (s MailService) RepairDelivery(ctx context.Context, request MailDeliveryRepair) (any, error) {
	if len(request.Items) < 1 || len(request.Items) > 10 {
		return nil, errors.New("provide 1..10 explicitly audited emails")
	}
	ids := []string{}
	err := s.Repository.transact(ctx, func(tx *sql.Tx) error {
		job, err := loadMorningJob(ctx, tx, request.JobID)
		if err != nil {
			return err
		}
		if !IsMailHealthTask(job) || job.Target == nil {
			return errors.New("repair only supports the audited legacy mail-health bypass")
		}
		var run heartbeat.Run
		for _, candidate := range job.History {
			if candidate.StartedAt == request.Attempt {
				run = candidate
			}
		}
		if run.Status != "delivered" || run.Result == "" {
			return errors.New("repair requires a confirmed delivered run")
		}
		owner := ConversationOwner(job.Target.ConnectionID, job.Target.ChatID, job.WorkspaceRoot)
		hash := sha256.Sum256([]byte(owner + "\x00" + fmt.Sprintf("%s:%d", job.ID, run.StartedAt)))
		var raw string
		if err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_notifications WHERE owner=? AND id=?", owner, fmt.Sprintf("%x", hash[:16])).Scan(&raw); err != nil {
			return err
		}
		var notification Notification
		if err := json.Unmarshal([]byte(raw), &notification); err != nil {
			return err
		}
		if notification.State != "delivered" || notification.Text != run.Result {
			return errors.New("run has no matching delivered outbox payload")
		}
		seen := map[string]bool{}
		for _, item := range request.Items {
			if item.ID == "" || seen[item.ID] || len([]rune(item.Anchor)) < 4 || !strings.Contains(run.Result, item.Anchor) {
				return errors.New("repair requires distinct IDs and exact visible anchors")
			}
			seen[item.ID] = true
			var receipt string
			if err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='mail_read_receipt' AND id=?", mailHash(fmt.Sprintf("%s\x00%d\x00%s", job.ID, run.StartedAt, item.ID))).Scan(&receipt); err != nil || receipt != "true" {
				return errors.New("email lacks the exact successful body-read receipt")
			}
			if err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='mail_message' AND id=?", item.ID).Scan(&raw); err != nil {
				return err
			}
			var record mailRecord
			if err := json.Unmarshal([]byte(raw), &record); err != nil {
				return err
			}
			if !strings.Contains(record.Message.Subject, item.Anchor) {
				return errors.New("visible anchor must also identify the audited email subject")
			}
			ids = append(ids, item.ID)
		}
		if request.Apply {
			for _, id := range ids {
				if err := recordMailDelivery(ctx, tx, job, run, id, "audited_legacy_health"); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return map[string]any{"applied": request.Apply && err == nil, "verified_mail_ids": ids, "analysis_history_changed": false, "messages_sent": 0}, err
}
