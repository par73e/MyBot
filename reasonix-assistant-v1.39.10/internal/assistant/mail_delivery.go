package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"reasonix/internal/heartbeat"
)

const MailHealthMessage = "Sir，有个邮箱连续连接失败，部分新邮件暂时可能无法及时提醒。恢复连接后会继续检查。"

func IsMailHealthTask(t heartbeat.Task) bool {
	return t.Capability == "mail" && len(t.MailIDs) == 0 && (t.ModuleID == "mail_health" || strings.HasPrefix(t.ID, "mail-health-"))
}

// This records a confirmed proactive notification, never human reading or reply.
type mailDelivery struct {
	ID          string `json:"id"`
	Dedup       string `json:"dedup"`
	JobID       string `json:"job_id"`
	Attempt     int64  `json:"attempt"`
	ConfirmedAt int64  `json:"confirmed_at"`
	Source      string `json:"source"`
}

type deliveryIndex map[string]mailDelivery

func (index deliveryIndex) contains(id, dedup string) bool {
	_, byID := index["id:"+id]
	_, byDedup := index["dedup:"+dedup]
	return byID || dedup != "" && byDedup
}

func mailDeliveryIndex(ctx context.Context, tx *sql.Tx) (deliveryIndex, error) {
	rows, err := loadEntities[mailDelivery](ctx, tx, "mail_delivery")
	index := deliveryIndex{}
	for _, row := range rows {
		index["id:"+row.ID] = row
		if row.Dedup != "" {
			index["dedup:"+row.Dedup] = row
		}
	}
	return index, err
}

func recordMailDelivery(ctx context.Context, tx *sql.Tx, t heartbeat.Task, r heartbeat.Run, id, source string) error {
	var raw string
	if err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='mail_message' AND id=?", id).Scan(&raw); err != nil {
		return err
	}
	var record mailRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return err
	}
	// Preserve the first confirmed notification rather than rewriting history.
	value, err := json.Marshal(mailDelivery{id, record.Dedup, t.ID, r.StartedAt, time.Now().UnixMilli(), source})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO assistant_entities(kind,id,payload) VALUES('mail_delivery',?,?)", id, string(value))
	return err
}

// Called only by the host after sending succeeds. Morning ACK uses the same
// ledger in its existing transaction, including only actually displayed IDs.
func (s MailService) AcknowledgeNotification(ctx context.Context, t heartbeat.Task, r heartbeat.Run) error {
	if t.Capability != "mail" || t.ModuleID == "mail_digest" || IsMailHealthTask(t) {
		return nil
	}
	return s.Repository.transact(ctx, func(tx *sql.Tx) error {
		decisions, err := loadEntities[MailDecision](ctx, tx, "mail_decision")
		if err != nil {
			return err
		}
		for _, decision := range decisions {
			if decision.JobID == t.ID && decision.Attempt == r.StartedAt && decision.Notify != nil && *decision.Notify && containsMailID(t.MailIDs, decision.ID) {
				if err := recordMailDelivery(ctx, tx, t, r, decision.ID, "mail_analysis"); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// Migrate only exact legacy per-message decisions with a successful matching
// run. Failed, silent, pending and ambiguous health-task prose prove nothing.
func reconcileMailDeliveries(ctx context.Context, tx *sql.Tx) error {
	jobs, err := loadEntities[heartbeat.Task](ctx, tx, "job")
	if err != nil {
		return err
	}
	decisions, err := loadEntities[MailDecision](ctx, tx, "mail_decision")
	if err != nil {
		return err
	}
	byJob := map[string]heartbeat.Task{}
	for _, job := range jobs {
		byJob[job.ID] = job
	}
	for _, decision := range decisions {
		t := byJob[decision.JobID]
		if t.Capability != "mail" || t.ModuleID == "mail_digest" || decision.Notify == nil || !*decision.Notify || !containsMailID(t.MailIDs, decision.ID) {
			continue
		}
		for _, run := range t.History {
			if run.Status == "delivered" && run.Result != "" && run.StartedAt == decision.Attempt {
				if err := recordMailDelivery(ctx, tx, t, run, decision.ID, "legacy_confirmed_run"); err != nil {
					return err
				}
			}
		}
	}
	return reconcileMorningMailDeliveries(ctx, tx, byJob)
}

type confirmedMorningMail struct {
	Snapshot MorningSnapshot `json:"snapshot"`
	Message  string          `json:"message"`
	IDs      []string        `json:"displayed_mail_ids"`
}

func reconcileMorningMailDeliveries(ctx context.Context, tx *sql.Tx, jobs map[string]heartbeat.Task) error {
	results, err := loadEntities[confirmedMorningMail](ctx, tx, "morning_delivered")
	if err != nil {
		return err
	}
	for _, result := range results {
		job := jobs[result.Snapshot.ID]
		if job.ModuleID != "morning_briefing" {
			continue
		}
		for _, run := range job.History {
			if run.Status != "delivered" || run.Result == "" || run.Result != result.Message {
				continue
			}
			for _, id := range result.IDs {
				if !containsMailID(job.MailIDs, id) {
					return errors.New("legacy morning displayed an unbound mail ID")
				}
				if err := recordMailDelivery(ctx, tx, job, run, id, "legacy_confirmed_morning"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func containsMailID(ids []string, id string) bool {
	for _, allowed := range ids {
		if allowed == id {
			return true
		}
	}
	return false
}

// A previously queued failed batch can contain IDs repaired after its creation.
// Rebind before invoking the model; do not reuse the old IDs in its prompt.
func (s MailService) PrepareAnalysis(ctx context.Context, t heartbeat.Task) (heartbeat.Task, error) {
	if len(t.MailIDs) == 0 {
		return t, errors.New("mail analysis requires host-bound email IDs")
	}
	err := s.Repository.transact(ctx, func(tx *sql.Tx) error {
		if err := reconcileMailDeliveries(ctx, tx); err != nil {
			return err
		}
		index, err := mailDeliveryIndex(ctx, tx)
		if err != nil {
			return err
		}
		ids := []string{}
		for _, id := range t.MailIDs {
			var raw string
			if err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='mail_message' AND id=?", id).Scan(&raw); err != nil {
				return err
			}
			var record mailRecord
			if err := json.Unmarshal([]byte(raw), &record); err != nil {
				return err
			}
			if !index.contains(id, record.Dedup) {
				ids = append(ids, id)
			}
		}
		if len(ids) == len(t.MailIDs) {
			return nil
		}
		stored, err := loadMorningJob(ctx, tx, t.ID)
		if err != nil {
			return err
		}
		encoded, _ := json.Marshal(ids)
		t.MailIDs = ids
		t.Prompt = MailAnalysisInstructions + "\nHost check time: " + time.Now().Format(time.RFC3339) + "\nNew email IDs (DATA): " + string(encoded)
		stored.MailIDs, stored.Prompt = t.MailIDs, t.Prompt
		return saveEntity(ctx, tx, "job", t.ID, stored)
	})
	return t, err
}

// Render only the ID-bound, validated summaries. Free-form top-level message
// cannot smuggle previously delivered or unrelated mail into this notification.
func (s MailService) FinalizeNotification(ctx context.Context, t heartbeat.Task, raw string) (string, error) {
	if err := s.RecordMailDecisions(ctx, t, raw); err != nil {
		return "", err
	}
	var result struct {
		Decisions []MailDecision `json:"mail_decisions"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return "", err
	}
	decisions, err := s.filterDeliveredDecisions(ctx, result.Decisions)
	if err != nil {
		return "", err
	}
	lines := []string{}
	for _, d := range decisions {
		if *d.Notify {
			lines = append(lines, d.Summary)
		}
	}
	message := strings.Join(lines, "\n")
	if message != "" && !strings.Contains(message, "Sir") {
		message = "Sir，" + message
	}
	return message, nil
}
