package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"reasonix/internal/heartbeat"
)

func digestSeed(t *testing.T, s MailService, id, status, result string, ids ...string) heartbeat.Task {
	t.Helper()
	j := heartbeat.Task{ID: id, Capability: "mail", MailIDs: ids, History: []heartbeat.Run{{StartedAt: 123, EndedAt: 124, Status: status, Result: result, DecisionReason: "not relevant"}}, RunStartedAt: 123}
	err := s.Repository.transact(t.Context(), func(tx *sql.Tx) error {
		for i, mid := range ids {
			if e := saveEntity(t.Context(), tx, "mail_message", mid, mailRecord{Message: MailMessage{ID: mid, Account: "qq", Subject: mid, ReceivedAt: int64(100 + i)}, JobID: id}); e != nil {
				return e
			}
		}
		return saveEntity(t.Context(), tx, "job", id, j)
	})
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func digestPage(t *testing.T, s MailService, r MailRequest) SilentMailPage {
	t.Helper()
	r.Action = "silent"
	v, e := s.Query(t.Context(), r)
	if e != nil {
		t.Fatal(e)
	}
	return v.(SilentMailPage)
}
func TestSilentMailMixedBatchAndFailures(t *testing.T) {
	s, _ := mailFixture(t)
	j := digestSeed(t, s, "mixed", "delivered", "notice", "silent", "notified")
	raw := `{"notify":true,"message":"notice","reason":"mixed","mail_decisions":[{"id":"silent","notify":false,"summary":"普通促销","reason":"无关"},{"id":"notified","notify":true,"summary":"需要确认登录","reason":"安全"}]}`
	if e := s.RecordMailDecisions(t.Context(), j, raw); e != nil {
		t.Fatal(e)
	}
	failed := digestSeed(t, s, "failed", "failed", "", "failed-mail")
	if e := s.RecordMailDecisions(t.Context(), failed, `{"notify":false,"mail_decisions":[{"id":"failed-mail","notify":false,"summary":"广告","reason":"无关"}]}`); e != nil {
		t.Fatal(e)
	}
	digestSeed(t, s, "pending", "running", "", "pending-mail")
	digestSeed(t, s, "delivery-failed", "delivery_failed", "some notice", "delivery-mail")
	digestSeed(t, s, "legacy-mixed", "delivered", "some notice", "unknown-mail")
	digestSeed(t, s, "legacy-silent", "completed", "", "legacy-mail")
	p := digestPage(t, s, MailRequest{})
	if p.Total != 2 {
		t.Fatalf("wrong silent population: %+v", p)
	}
	for _, v := range p.Items {
		if v.Message.ID != "silent" && v.Message.ID != "legacy-mail" {
			t.Fatal("failure/unknown incorrectly silent", v)
		}
	}
	if p := digestPage(t, s, MailRequest{Since: "1970-01-01T00:01:41Z"}); p.Total != 0 {
		t.Fatal("received-time filter ignored")
	}
}
func TestMailDecisionRejectsMissingExtraDuplicateAndMismatch(t *testing.T) {
	s, _ := mailFixture(t)
	j := digestSeed(t, s, "job", "running", "", "a", "b")
	for _, raw := range []string{
		`{"notify":false}`,
		`{"notify":false,"mail_decisions":[{"id":"a","notify":false,"summary":"x","reason":"y"},{"id":"a","notify":false,"summary":"x","reason":"y"}]}`,
		`{"notify":false,"mail_decisions":[{"id":"a","notify":false,"summary":"x","reason":"y"},{"id":"outsider","notify":false,"summary":"x","reason":"y"}]}`,
		`{"notify":false,"mail_decisions":[{"id":"a","notify":false,"summary":"x","reason":"y"},{"id":"b","notify":true,"summary":"x","reason":"y"}]}`,
	} {
		if e := s.RecordMailDecisions(t.Context(), j, raw); e == nil {
			t.Fatal("accepted invalid decision", raw)
		}
	}
	if p := digestPage(t, s, MailRequest{}); p.Total != 0 {
		t.Fatal("invalid analysis became silent")
	}
}
func TestDigestSnapshotReadsAndDeliveredProgress(t *testing.T) {
	s, _ := mailFixture(t)
	digestSeed(t, s, "old", "completed", "", "a", "b")
	t.Setenv("REASONIX_ASSISTANT_MAIL_DIGEST", "1")
	t.Setenv("REASONIX_ASSISTANT_MAIL_BACKGROUND", "1")
	t.Setenv("REASONIX_ASSISTANT_JOB_ID", "digest")
	t.Setenv("REASONIX_ASSISTANT_MAIL_ATTEMPT", "456")
	task := heartbeat.Task{ID: "digest", ModuleID: "mail_digest", RunStartedAt: 456}
	// The host claims an enabled running job before invoking any background tools.
	if err := s.Repository.transact(t.Context(), func(tx *sql.Tx) error {
		return saveEntity(t.Context(), tx, "job", task.ID, heartbeat.Task{ID: task.ID, ModuleID: task.ModuleID, Enabled: true, History: []heartbeat.Run{{StartedAt: 456, Status: "running"}}})
	}); err != nil {
		t.Fatal(err)
	}
	final := `{"notify":true,"message":"两封邮件","reason":"回顾","digest_ids":["a","b"]}`
	if e := s.VerifyDigest(t.Context(), task, final); e == nil {
		t.Fatal("accepted no actual silent query")
	}
	p := digestPage(t, s, MailRequest{})
	if p.Total != 2 {
		t.Fatal(p)
	}
	if e := s.VerifyDigest(t.Context(), task, final); e == nil {
		t.Fatal("accepted no actual body reads")
	}
	for _, id := range []string{"a", "b"} {
		if e := s.recordMailRead(t.Context(), id); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.VerifyDigest(t.Context(), task, final); e != nil {
		t.Fatal(e)
	}
	if e := s.VerifyDigest(t.Context(), task, `{"notify":true,"digest_ids":["a","a"]}`); e == nil {
		t.Fatal("accepted duplicate digest IDs")
	}
	if p := digestPage(t, s, MailRequest{}); p.Total != 2 {
		t.Fatal("reading advanced progress")
	}
	if e := s.AcknowledgeDigest(t.Context(), task, heartbeat.Run{StartedAt: 456}); e != nil {
		t.Fatal(e)
	}
	// Separate durable marker survives restart and arbitrarily truncated history.
	t.Setenv("REASONIX_ASSISTANT_MAIL_DIGEST", "")
	restarted := NewMailService(s.Home)
	if p := digestPage(t, restarted, MailRequest{PendingOnly: true}); p.Total != 0 {
		t.Fatal("delivered digest replayed")
	}
	if p := digestPage(t, restarted, MailRequest{}); p.Total != 2 {
		t.Fatal("manual history disappeared")
	}
}
func TestDigestPaginationDoesNotAcknowledgeUnsentRemainder(t *testing.T) {
	s, _ := mailFixture(t)
	ids := []string{}
	for i := range 23 {
		ids = append(ids, fmt.Sprintf("m%02d", i))
	}
	digestSeed(t, s, "legacy", "completed", "", ids...)
	t.Setenv("REASONIX_ASSISTANT_MAIL_DIGEST", "1")
	t.Setenv("REASONIX_ASSISTANT_JOB_ID", "digest")
	t.Setenv("REASONIX_ASSISTANT_MAIL_ATTEMPT", "456")
	p := digestPage(t, s, MailRequest{})
	if len(p.Items) != 20 || !p.HasMore || p.Total != 23 {
		t.Fatal(p)
	}
	if e := s.AcknowledgeDigest(t.Context(), heartbeat.Task{ID: "digest", ModuleID: "mail_digest"}, heartbeat.Run{StartedAt: 456}); e != nil {
		t.Fatal(e)
	}
	t.Setenv("REASONIX_ASSISTANT_MAIL_ATTEMPT", "789")
	p = digestPage(t, s, MailRequest{})
	if p.Total != 3 || len(p.Items) != 3 {
		t.Fatal("lost remainder", p)
	}
}
func TestMailReadReceiptRequiresAllAvailablePages(t *testing.T) {
	s, _ := mailFixture(t)
	t.Setenv("REASONIX_ASSISTANT_MAIL_BACKGROUND", "1")
	t.Setenv("REASONIX_ASSISTANT_JOB_ID", "j")
	t.Setenv("REASONIX_ASSISTANT_MAIL_ATTEMPT", "123")
	if e := s.recordMailBodyPage(t.Context(), "a", 0, 8000, 16001); e != nil {
		t.Fatal(e)
	}
	if e := s.VerifyMailReads(t.Context(), "j", 123, []string{"a"}); e == nil {
		t.Fatal("first page accepted as full read")
	}
	if e := s.recordMailBodyPage(t.Context(), "a", 16000, 16001, 16001); e != nil {
		t.Fatal(e)
	}
	if e := s.VerifyMailReads(t.Context(), "j", 123, []string{"a"}); e == nil {
		t.Fatal("gap accepted")
	}
	if e := s.recordMailBodyPage(t.Context(), "a", 8000, 16000, 16001); e != nil {
		t.Fatal(e)
	}
	if e := s.VerifyMailReads(t.Context(), "j", 123, []string{"a"}); e != nil {
		t.Fatal(e)
	}
}
func TestDigestModuleSchedulingUsesSharedWindow(t *testing.T) {
	s, tool := unifiedFixture(t)
	c, _ := s.Config(t.Context())
	callHeartbeat(t, tool, fmt.Sprintf(`{"action":"set_module_enabled","revision":%d,"module_id":"mail_digest","enabled":true}`, c.Revision))
	c, _ = s.Config(t.Context())
	callHeartbeat(t, tool, fmt.Sprintf(`{"action":"set_trigger","revision":%d,"trigger_id":"review","title":"邮件回顾","mode":"daily","modules":["mail_digest"],"routine_bound":true}`, c.Revision))
	jobs := atHeartbeat(t, s, "2026-10-24T11:00:00+08:00")
	found := false
	for _, j := range jobs {
		if j.ModuleID == "mail_digest" {
			found = true
			if j.Capability != "mail" || !strings.Contains(j.Prompt, "逐封") {
				t.Fatal(j)
			}
			ok, e := s.Allows(context.Background(), j, mustTime(t, "2026-10-24T11:00:00+08:00"))
			if e != nil || !ok {
				t.Fatal(ok, e)
			}
		}
	}
	if !found {
		t.Fatal("digest trigger missing")
	}
}

// Keep test timestamps explicit and independent of the machine clock.
func mustTime(t *testing.T, raw string) time.Time {
	t.Helper()
	v, e := time.Parse(time.RFC3339, raw)
	if e != nil {
		t.Fatal(e)
	}
	return v
}

func TestMailDecisionContractExtension(t *testing.T) {
	raw := `{"notify":false,"message":"","reason":"no","mail_decisions":[{"id":"a","notify":false,"summary":"x","reason":"y"}]}`
	if _, e := heartbeat.NotificationText(raw); e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	if e := json.Unmarshal([]byte(raw), &v); e != nil {
		t.Fatal(e)
	}
}

func TestDigestCatalogUpgradePreservesTimingAndIsIdempotent(t *testing.T) {
	s, _ := unifiedFixture(t)
	c, _ := s.Config(t.Context())
	delete(c.ModuleEnabled, "mail_digest")
	before, _ := json.Marshal(c)
	if e := s.repo().transact(t.Context(), func(tx *sql.Tx) error { return saveEntity(t.Context(), tx, "heartbeat_config", heartbeatConfigID, c) }); e != nil {
		t.Fatal(e)
	}
	upgraded, e := s.Ensure(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if !upgraded.ModuleEnabled["mail_digest"] || upgraded.Revision != c.Revision+1 {
		t.Fatal(upgraded)
	}
	again, e := s.Ensure(t.Context())
	if e != nil || again.Revision != upgraded.Revision {
		t.Fatal(again, e)
	}
	delete(upgraded.ModuleEnabled, "mail_digest")
	upgraded.Revision = c.Revision
	after, _ := json.Marshal(upgraded)
	if string(before) != string(after) {
		t.Fatal("catalog upgrade changed timings or module selection")
	}
}
