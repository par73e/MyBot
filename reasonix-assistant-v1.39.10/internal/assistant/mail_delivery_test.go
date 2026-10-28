package assistant

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"reasonix/internal/heartbeat"
)

func deliveryFixture(t *testing.T) (MailService, heartbeat.Task) {
	t.Helper()
	s, options := mailFixture(t)
	s.Transport = func(_ context.Context, request map[string]any) (mailWire, error) {
		if request["action"] == "read" {
			return mailWire{Results: []mailResult{{Account: "qq", OK: true, Body: "Complete mail body"}}}, nil
		}
		return mailWire{Results: []mailResult{goodResult("qq",
			MailMessage{TransportID: "1", Subject: "Cloudflare security", MessageID: "<first>", ReceivedAt: 1},
			MailMessage{TransportID: "2", Subject: "TenZ streaming", MessageID: "<second>", ReceivedAt: 2}), goodResult("gmail1"), goodResult("gmail2")}}, nil
	}
	if _, err := s.Poll(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	job := jobsForMail(t, s)[0]
	job.RunStartedAt = 123
	job.History = []heartbeat.Run{{StartedAt: 123, Status: "running"}}
	if err := s.Repository.transact(t.Context(), func(tx *sql.Tx) error { return saveEntity(t.Context(), tx, "job", job.ID, job) }); err != nil {
		t.Fatal(err)
	}
	return s, job
}

func deliveries(t *testing.T, s MailService) deliveryIndex {
	t.Helper()
	var index deliveryIndex
	if err := s.Repository.transact(t.Context(), func(tx *sql.Tx) error { var err error; index, err = mailDeliveryIndex(t.Context(), tx); return err }); err != nil {
		t.Fatal(err)
	}
	return index
}

func TestMailBackgroundScopeAndHostRenderedNotification(t *testing.T) {
	s, job := deliveryFixture(t)
	t.Setenv("REASONIX_ASSISTANT_MAIL_BACKGROUND", "1")
	t.Setenv("REASONIX_ASSISTANT_JOB_ID", job.ID)
	t.Setenv("REASONIX_ASSISTANT_MAIL_ATTEMPT", "123")
	for _, request := range []MailRequest{{Action: "list"}, {Action: "search", Query: "old"}, {Action: "silent"}, {Action: "read", ID: "foreign-id"}} {
		if _, err := s.Query(t.Context(), request); err == nil {
			t.Fatal("background expanded its batch", request)
		}
	}
	for _, id := range job.MailIDs {
		if _, err := s.Query(t.Context(), MailRequest{Action: "read", ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(map[string]any{"notify": true, "message": "Unrelated old mail must not be repeated", "mail_decisions": []map[string]any{
		{"id": job.MailIDs[0], "notify": true, "summary": "新的安全通知", "reason": "new"},
		{"id": job.MailIDs[1], "notify": false, "summary": "普通广告", "reason": "silent"},
	}})
	text, err := s.FinalizeNotification(t.Context(), job, string(raw))
	if err != nil || text != "Sir，新的安全通知" {
		t.Fatal(text, err)
	}
	if len(deliveries(t, s)) != 0 {
		t.Fatal("analysis pretended successful delivery")
	}
	if err := s.AcknowledgeNotification(t.Context(), job, heartbeat.Run{StartedAt: 123}); err != nil {
		t.Fatal(err)
	}
	index := deliveries(t, s)
	if !index.contains(job.MailIDs[0], "") || index.contains(job.MailIDs[1], "") {
		t.Fatal("ACK marked silent mail as notified", index)
	}
	text, err = s.FinalizeNotification(t.Context(), job, string(raw))
	if err != nil || text != "" {
		t.Fatal("already notified mail repeated", text, err)
	}
	prepared, err := s.PrepareAnalysis(t.Context(), job)
	if err != nil || len(prepared.MailIDs) != 1 || prepared.MailIDs[0] != job.MailIDs[1] || strings.Contains(prepared.Prompt, job.MailIDs[0]) {
		t.Fatal("retry kept notified IDs", prepared.MailIDs, err)
	}
}

func TestMailLegacyReconciliationRequiresExactSuccessfulDecision(t *testing.T) {
	for _, state := range []string{"delivered", "completed", "failed", "interrupted", "delivery_pending", "delivery_uncertain", "delivery_failed"} {
		t.Run(state, func(t *testing.T) {
			s, job := deliveryFixture(t)
			yes := true
			err := s.Repository.transact(t.Context(), func(tx *sql.Tx) error {
				job.History = []heartbeat.Run{{StartedAt: 123, Status: state, Result: "notice"}}
				if err := saveEntity(t.Context(), tx, "job", job.ID, job); err != nil {
					return err
				}
				if err := saveEntity(t.Context(), tx, "mail_decision", job.MailIDs[0], MailDecision{ID: job.MailIDs[0], JobID: job.ID, Attempt: 123, Notify: &yes}); err != nil {
					return err
				}
				return reconcileMailDeliveries(t.Context(), tx)
			})
			if err != nil {
				t.Fatal(err)
			}
			if deliveries(t, s).contains(job.MailIDs[0], "") != (state == "delivered") {
				t.Fatal("unconfirmed run marked as notified", state)
			}
		})
	}
}

func TestMorningAndStandaloneMailShareConfirmedLedger(t *testing.T) {
	_, morning, job, out := readyMorningDraft(t)
	message, err := morning.Finalize(t.Context(), job, mustJSON(t, out))
	if err != nil {
		t.Fatal(err)
	}
	mail := NewMailService(morning.Home)
	if len(deliveries(t, mail)) != 0 {
		t.Fatal("morning draft marked delivered")
	}
	if err := morning.Acknowledge(t.Context(), job, heartbeat.Run{StartedAt: job.RunStartedAt, Result: message}); err != nil {
		t.Fatal(err)
	}
	for _, decision := range out.MailDecisions {
		if deliveries(t, mail).contains(decision.ID, "") != *decision.Notify {
			t.Fatal("morning ledger differs from displayed mail", decision.ID)
		}
	}
	filtered, err := mail.filterDeliveredDecisions(t.Context(), out.MailDecisions)
	if err != nil {
		t.Fatal(err)
	}
	for _, decision := range filtered {
		if *decision.Notify {
			t.Fatal("morning delivered mail remained a candidate")
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestAuditedHealthRepairRequiresOutboxBodyReadAndSubjectEvidence(t *testing.T) {
	s, original := deliveryFixture(t)
	job := original
	job.ID = "mail-health-audited"
	job.MailIDs = nil
	job.History = []heartbeat.Run{{StartedAt: 456, Status: "delivered", Result: "Cloudflare security; TenZ streaming"}}
	if err := s.Repository.transact(t.Context(), func(tx *sql.Tx) error {
		if err := saveEntity(t.Context(), tx, "job", job.ID, job); err != nil {
			return err
		}
		for _, id := range original.MailIDs {
			if err := saveEntity(t.Context(), tx, "mail_read_receipt", mailHash(fmt.Sprintf("%s\x00%d\x00%s", job.ID, 456, id)), true); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	owner := ConversationOwner(job.Target.ConnectionID, job.Target.ChatID, job.WorkspaceRoot)
	hash := sha256.Sum256([]byte(owner + "\x00" + fmt.Sprintf("%s:%d", job.ID, 456)))
	n, err := s.Repository.PrepareNotification(t.Context(), owner, Notification{ID: fmt.Sprintf("%x", hash[:16]), SessionID: "fixture", Text: job.History[0].Result})
	if err != nil {
		t.Fatal(err)
	}
	request := MailDeliveryRepair{JobID: job.ID, Attempt: 456, Items: []MailDeliveryRepairItem{{original.MailIDs[0], "Cloudflare"}, {original.MailIDs[1], "TenZ"}}}
	if _, err := s.RepairDelivery(t.Context(), request); err == nil {
		t.Fatal("unconfirmed outbox accepted")
	}
	if err := s.Repository.NotificationState(t.Context(), owner, n, "delivered"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepairDelivery(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if len(deliveries(t, s)) != 0 {
		t.Fatal("dry run wrote ledger")
	}
	if err := s.Repository.transact(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), "DELETE FROM assistant_entities WHERE kind='mail_read_receipt' AND id=?", mailHash(fmt.Sprintf("%s\x00%d\x00%s", job.ID, 456, original.MailIDs[0])))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepairDelivery(t.Context(), request); err == nil {
		t.Fatal("missing body-read receipt accepted")
	}
	if err := s.Repository.transact(t.Context(), func(tx *sql.Tx) error {
		return saveEntity(t.Context(), tx, "mail_read_receipt", mailHash(fmt.Sprintf("%s\x00%d\x00%s", job.ID, 456, original.MailIDs[0])), true)
	}); err != nil {
		t.Fatal(err)
	}
	request.Apply = true
	for range 2 {
		if _, err := s.RepairDelivery(t.Context(), request); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range original.MailIDs {
		if !deliveries(t, s).contains(id, "") {
			t.Fatal("confirmed bypass missing", id)
		}
	}
	request.Items[0].Anchor = "missing-anchor"
	if _, err := s.RepairDelivery(t.Context(), request); err == nil {
		t.Fatal("invented display evidence accepted")
	}
}

type mailDeliveryRunner func(context.Context, heartbeat.Task) (heartbeat.Execution, error)

func (run mailDeliveryRunner) Run(ctx context.Context, task heartbeat.Task) (heartbeat.Execution, error) {
	return run(ctx, task)
}

type mailDeliverySender struct {
	err   error
	calls int
}

func (s *mailDeliverySender) Send(context.Context, heartbeat.Target, string) error {
	s.calls++
	return s.err
}

func TestMailLedgerWaitsForSendAndSurvivesRestartWithoutReply(t *testing.T) {
	s, job := deliveryFixture(t)
	now := time.Now()
	job.History = nil
	job.LastRunAt = 0
	job.At = now.Add(-time.Second).Format(time.RFC3339)
	store := JobStore{Repository: s.Repository}
	if err := s.Repository.transact(t.Context(), func(tx *sql.Tx) error { return saveEntity(t.Context(), tx, "job", job.ID, job) }); err != nil {
		t.Fatal(err)
	}
	calls := 0
	runner := mailDeliveryRunner(func(ctx context.Context, task heartbeat.Task) (heartbeat.Execution, error) {
		calls++
		raw := mustJSON(t, map[string]any{"notify": true, "mail_decisions": []map[string]any{
			{"id": task.MailIDs[0], "notify": true, "summary": "安全通知", "reason": "needs attention"},
			{"id": task.MailIDs[1], "notify": false, "summary": "普通邮件", "reason": "silent"},
		}})
		text, err := s.FinalizeNotification(ctx, task, raw)
		return heartbeat.Execution{Text: text}, err
	})
	sender := &mailDeliverySender{err: errors.New("transport failed before send")}
	engine := heartbeat.Engine{Store: store, Runner: runner, Sender: sender, Clock: func() time.Time { return now }, AfterDelivery: s.AcknowledgeNotification}
	if err := engine.Tick(t.Context(), now); err == nil || len(deliveries(t, s)) != 0 {
		t.Fatal("failed send wrote notification ledger", err)
	}
	sender.err = nil
	now = now.Add(time.Minute)
	if err := engine.Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || sender.calls != 2 || !deliveries(t, s).contains(job.MailIDs[0], "") || deliveries(t, s).contains(job.MailIDs[1], "") {
		t.Fatal("retry reran analysis or incorrectly acknowledged mail", calls, sender.calls)
	}
	restarted := NewMailService(s.Home)
	if !deliveries(t, restarted).contains(job.MailIDs[0], "") {
		t.Fatal("restart lost confirmed delivery")
	}
	// A forwarded/native duplicate is also covered by the persistent dedup key.
	if err := s.Repository.transact(t.Context(), func(tx *sql.Tx) error {
		var raw string
		if err := tx.QueryRowContext(t.Context(), "SELECT payload FROM assistant_entities WHERE kind='mail_message' AND id=?", job.MailIDs[0]).Scan(&raw); err != nil {
			return err
		}
		var record mailRecord
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return err
		}
		record.Message.ID, record.JobID = "forwarded-copy", ""
		pending := map[string]mailRecord{record.Message.ID: record}
		n, err := queueMailBatch(t.Context(), tx, pending, Options{Workspace: s.Home, Target: job.Target}, now)
		if n != 0 {
			t.Fatal("forwarded copy was requeued")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
