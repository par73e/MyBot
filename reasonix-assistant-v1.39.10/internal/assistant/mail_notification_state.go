package assistant

import (
	"context"
	"database/sql"

	"reasonix/internal/heartbeat"
)

func (s MailService) filterDeliveredDecisions(ctx context.Context, decisions []MailDecision) ([]MailDecision, error) {
	if decisions == nil {
		return nil, nil
	}
	result := append([]MailDecision{}, decisions...)
	err := s.Repository.transact(ctx, func(tx *sql.Tx) error {
		index, err := mailDeliveryIndex(ctx, tx)
		if err != nil {
			return err
		}
		records, err := loadEntities[mailRecord](ctx, tx, "mail_message")
		if err != nil {
			return err
		}
		dedup := map[string]string{}
		for _, record := range records {
			dedup[record.Message.ID] = record.Dedup
		}
		for i, d := range result {
			if index.contains(d.ID, dedup[d.ID]) {
				no := false
				result[i].Notify, result[i].TaskCandidates = &no, nil
				result[i].Reason = "此前已确认通知，不重复播报"
			}
		}
		return nil
	})
	return result, err
}

func (s MailService) annotateMailNotifications(ctx context.Context, messages []MailMessage, records []mailRecord) error {
	return s.Repository.transact(ctx, func(tx *sql.Tx) error {
		index, err := mailDeliveryIndex(ctx, tx)
		if err != nil {
			return err
		}
		decisions, err := loadEntities[MailDecision](ctx, tx, "mail_decision")
		if err != nil {
			return err
		}
		jobs, err := loadEntities[heartbeat.Task](ctx, tx, "job")
		if err != nil {
			return err
		}
		byID, byJob, byDecision := map[string]mailRecord{}, map[string]heartbeat.Task{}, map[string]MailDecision{}
		for _, r := range records {
			byID[r.Message.ID] = r
		}
		for _, j := range jobs {
			byJob[j.ID] = j
		}
		for _, d := range decisions {
			byDecision[d.ID] = d
		}
		for i, message := range messages {
			r := byID[message.ID]
			status := "untracked"
			if r.JobID != "" {
				status = "pending"
			}
			job := byJob[r.JobID]
			if len(job.History) > 0 {
				run := job.History[len(job.History)-1]
				if run.Status == "failed" || run.Status == "interrupted" {
					status = "analysis_failed"
				}
				d := byDecision[message.ID]
				if d.Attempt == run.StartedAt && d.JobID == job.ID && d.Notify != nil && !*d.Notify && successfulAnalysis(run) {
					status = "silent"
				}
			}
			if index.contains(message.ID, r.Dedup) {
				status = "notified"
			}
			messages[i].NotificationStatus = status
		}
		return nil
	})
}
