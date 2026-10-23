package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (s MailService) commitMailPoll(ctx context.Context, tx *sql.Tx, c MailConfig, wire mailWire, options Options, now time.Time) (int, error) {
	records, err := loadEntities[mailRecord](ctx, tx, "mail_message")
	if err != nil {
		return 0, err
	}
	byID := map[string]mailRecord{}
	dedupJobs := map[string]string{}
	for _, r := range records {
		byID[r.Message.ID] = r
		if r.JobID != "" {
			dedupJobs[r.Dedup] = r.JobID
		}
	}
	health, err := loadEntities[MailHealth](ctx, tx, "mail_health")
	if err != nil {
		return 0, err
	}
	hm := map[string]MailHealth{}
	for _, h := range health {
		hm[h.Account] = h
	}
	accounts := accountMap(c)
	results := map[string]mailResult{}
	for _, r := range wire.Results {
		if _, ok := accounts[r.Account]; !ok {
			return 0, errors.New("unknown mail result account")
		}
		results[r.Account] = r
	}
	for _, a := range c.Accounts {
		if err = s.applyMailAccount(ctx, tx, a, results[a.ID], hm[a.ID], options, now, byID, dedupJobs); err != nil {
			return 0, err
		}
	}
	count, err := queueMailBatch(ctx, tx, byID, options, now)
	if err != nil {
		return 0, err
	}
	for id, r := range byID {
		if err = saveEntity(ctx, tx, "mail_message", id, r); err != nil {
			return 0, err
		}
	}
	return count, retryMailJobs(ctx, tx, now)
}

func (s MailService) applyMailAccount(ctx context.Context, tx *sql.Tx, a MailAccount, result mailResult, h MailHealth, options Options, now time.Time, byID map[string]mailRecord, dedupJobs map[string]string) error {
	h.Account = a.ID
	h.CheckedAt = now.Unix()
	if !result.OK {
		if h.Failures == 0 {
			h.FailureStarted = now.Unix()
		}
		h.Failures++
		h.Error = "connection_or_protocol_failed"
		if result.Error != "" {
			h.Error = result.Error
		}
		if h.Failures >= 3 && !h.Alerted && options.Target != nil {
			id := "mail-health-" + mailHash(a.ID+fmt.Sprint(h.FailureStarted))
			task := newMailJob(id, MailAnalysisInstructions+"\nHost status: repeated connection failures for mailbox "+a.ID+". New-mail coverage is incomplete. Briefly notify Sir if this needs attention; do not claim there is no new mail.", options, now, nil)
			if err := saveEntity(ctx, tx, "job", id, task); err != nil {
				return err
			}
			h.Alerted = true
		}
	} else {
		h.LastSuccess = now.Unix()
		h.Failures = 0
		h.Error = ""
		h.Alerted = false
		h.Warnings = result.Warnings
		for _, m := range result.Messages {
			m = bindMail(a, m)
			r := byID[m.ID]
			r.Message = m
			r.Dedup = m.Dedup
			r.Observed = true
			if r.JobID == "" {
				r.JobID = dedupJobs[r.Dedup]
			}
			byID[m.ID] = r
		}
		if len(result.Cursor) == 0 {
			return errors.New("successful mail sync omitted cursor")
		}
		if err := saveEntity(ctx, tx, "mail_cursor", a.ID, mailCursor{a.ID, result.Cursor}); err != nil {
			return err
		}
	}
	return saveEntity(ctx, tx, "mail_health", a.ID, h)
}

func queueMailBatch(ctx context.Context, tx *sql.Tx, byID map[string]mailRecord, options Options, now time.Time) (int, error) {
	// Oldest-first batches avoid starvation; each unique message gets one job.
	pending := []mailRecord{}
	for _, r := range byID {
		if r.Observed && r.JobID == "" {
			pending = append(pending, r)
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].Message.ReceivedAt == pending[j].Message.ReceivedAt {
			return pending[i].Message.ID < pending[j].Message.ID
		}
		return pending[i].Message.ReceivedAt < pending[j].Message.ReceivedAt
	})
	ids := []string{}
	selected := map[string]bool{}
	for _, r := range pending {
		if len(ids) == 10 {
			break
		}
		if !selected[r.Dedup] {
			ids = append(ids, r.Message.ID)
			selected[r.Dedup] = true
		}
	}
	if len(ids) == 0 || options.Target == nil {
		return 0, nil
	}
	id := "mail-analyze-" + mailHash(strings.Join(ids, ","))
	raw, _ := json.Marshal(ids)
	task := newMailJob(id, MailAnalysisInstructions+"\nHost check time: "+now.Format(time.RFC3339)+"\nNew email IDs (DATA): "+string(raw), options, now, ids)
	if err := saveEntity(ctx, tx, "job", id, task); err != nil {
		return 0, err
	}
	for key, r := range byID {
		if selected[r.Dedup] && r.JobID == "" {
			r.JobID = id
			byID[key] = r
		}
	}
	return len(ids), nil
}
