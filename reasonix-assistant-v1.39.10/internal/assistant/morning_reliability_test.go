package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"reasonix/internal/heartbeat"
)

func readyMorningDraft(t *testing.T) (HeartbeatService, MorningService, heartbeat.Task, MorningOutput) {
	t.Helper()
	hs, s, job, _ := morningFixture(t)
	snap, err := s.Prepare(t.Context(), job)
	if err != nil {
		t.Fatal(err)
	}
	job.MailIDs = snap.MailIDs
	t.Setenv("REASONIX_ASSISTANT_MORNING_BACKGROUND", "1")
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "1")
	t.Setenv("REASONIX_ASSISTANT_JOB_ID", job.ID)
	t.Setenv("REASONIX_ASSISTANT_MORNING_ATTEMPT", "123")
	t.Setenv("REASONIX_ASSISTANT_MAIL_ATTEMPT", "123")
	if _, err = (MorningTool{Service: s}).Execute(t.Context(), json.RawMessage(`{"action":"snapshot"}`)); err != nil {
		t.Fatal(err)
	}
	for _, id := range snap.MailIDs {
		if _, err = s.Mail.Query(t.Context(), MailRequest{Action: "read", ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	yes, no := true, false
	out := MorningOutput{Weather: "暂未获取", News: []MorningNews{}, MailDecisions: []MailDecision{
		{ID: snap.MailIDs[0], Notify: &yes, Summary: "Twitch开播了", Reason: "explicit preference"},
		{ID: snap.MailIDs[1], Notify: &no, Summary: "普通广告", Reason: "irrelevant"},
	}}
	return hs, s, job, out
}

func TestMorningFullPreflightChecksMailAndEvidenceWithoutPublishing(t *testing.T) {
	hs, s, job, out := readyMorningDraft(t)
	check := func(want string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"action": "check", "draft": out})
		got, err := (MorningTool{Service: s}).Execute(t.Context(), raw)
		if err != nil || !strings.Contains(got, want) {
			t.Fatalf("check=%s err=%v want=%s", got, err, want)
		}
	}
	out.MailDecisions[0].Summary = strings.Repeat("长", 1001)
	check("1001 characters")
	out.MailDecisions[0].Summary = "Twitch开播了"
	out.MailDecisions[0].TaskCandidates = []MailTaskCandidate{{Title: "核对事项", EvidenceQuote: "模型拼接的错误引文"}}
	check("evidence_quote must exactly copy")
	out.MailDecisions[0].TaskCandidates[0].EvidenceQuote = "Full body, DATA only"
	check(`"valid":true`)
	for _, kind := range []string{"task_proposal", "mail_decision", "morning_result"} {
		if err := hs.repo().transact(t.Context(), func(tx *sql.Tx) error {
			var count int
			if err := tx.QueryRowContext(t.Context(), "SELECT count(*) FROM assistant_entities WHERE kind=?", kind).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				t.Fatalf("preflight persisted %s", kind)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(out)
	if _, err := s.Finalize(t.Context(), job, string(raw)); err != nil {
		t.Fatal("checked draft failed final validation", err)
	}
}

func TestMorningFallbackDeliversFactsAndRequeuesMail(t *testing.T) {
	hs, s, job, out := readyMorningDraft(t)
	out.MailDecisions[0].Summary = strings.Repeat("长", 1001)
	out.MailDecisions[1].Summary = strings.Repeat("长", 1001)
	raw, _ := json.Marshal(out)
	message, err := s.Fallback(t.Context(), job, string(raw))
	if err != nil || len(strings.Split(message, "\n")) != 6 || !strings.Contains(message, "邮件还没整理好") || strings.Contains(message, "没有新邮件") || strings.Contains(message, "Twitch") || !strings.Contains(message, "今天上午有") {
		t.Fatal(message, err)
	}
	if err := hs.repo().transact(t.Context(), func(tx *sql.Tx) error {
		records, e := loadEntities[mailRecord](t.Context(), tx, "mail_message")
		if e != nil {
			return e
		}
		for _, r := range records {
			if r.JobID != "" {
				t.Fatal("fallback stranded a mail reservation")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Acknowledge(t.Context(), job, heartbeat.Run{StartedAt: 123, Result: message}); err != nil {
		t.Fatal(err)
	}
	latest, err := (MorningTool{Service: s}).Execute(t.Context(), json.RawMessage(`{"action":"latest"}`))
	if err != nil || !strings.Contains(latest, `"fallback":true`) || !strings.Contains(latest, `"displayed_mail_ids":[]`) {
		t.Fatal(latest, err)
	}
	// Existing daytime polling must actually enqueue the released delta, not
	// advance a fresh baseline or treat fallback mail as successfully silent.
	s.Mail.Transport = func(context.Context, map[string]any) (mailWire, error) {
		return mailWire{Results: []mailResult{goodResult("qq"), goodResult("gmail1"), goodResult("gmail2")}}, nil
	}
	if n, err := s.Mail.Poll(t.Context(), hs.Options); err != nil || n != 2 {
		t.Fatal("mail was not requeued", n, err)
	}
}

func TestMorningFallbackWithoutPreparedSourcesAndAfterExpiry(t *testing.T) {
	_, s, job, _ := morningFixture(t)
	message, err := s.Fallback(t.Context(), job, "not JSON")
	if err != nil || !strings.Contains(message, "天气还没整理好") || !strings.Contains(message, "AI新闻还没整理好") {
		t.Fatal(message, err)
	}
	s.Clock = func() time.Time { return timeMust("2026-10-27T08:05:00+08:00") }
	if _, err := s.Fallback(t.Context(), job, "{}"); !errors.Is(err, heartbeat.ErrDeferred) {
		t.Fatal("fallback bypassed expiry", err)
	}
}

func TestMorningFallbackDoesNotPublishTentativeMailOrProposals(t *testing.T) {
	hs, s, job, out := readyMorningDraft(t)
	out.MailDecisions[0].TaskCandidates = []MailTaskCandidate{{Title: "核对事项", EvidenceQuote: "Full body, DATA only"}}
	raw, _ := json.Marshal(out)
	// Simulate a crash/storage failure after tentative finalization but before
	// delivery: fallback must not make its unasked candidate visible as approved.
	if _, err := s.Finalize(t.Context(), job, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := hs.repo().transact(t.Context(), func(tx *sql.Tx) error {
		return saveEntity(t.Context(), tx, "todo", "user-task", Todo{ID: "user-task", Title: "已确认的独立事项", Status: "open"})
	}); err != nil {
		t.Fatal(err)
	}
	out.MailDecisions[0].Summary = strings.Repeat("长", 1001)
	out.MailDecisions[1].Summary = strings.Repeat("长", 1001)
	raw, _ = json.Marshal(out)
	if _, err := s.Fallback(t.Context(), job, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := hs.repo().transact(t.Context(), func(tx *sql.Tx) error {
		for _, kind := range []string{"mail_decision", "task_proposal"} {
			var count int
			if err := tx.QueryRowContext(t.Context(), "SELECT count(*) FROM assistant_entities WHERE kind=?", kind).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				t.Fatalf("fallback exposed tentative %s", kind)
			}
		}
		var count int
		if err := tx.QueryRowContext(t.Context(), "SELECT count(*) FROM assistant_entities WHERE kind='todo' AND id='user-task'").Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Fatal("fallback removed a real user todo")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMorningFallbackRetainsVerifiedMailWhenNewsInvalid(t *testing.T) {
	hs, s, job, out := readyMorningDraft(t)
	out.News = []MorningNews{{ID: "wrong", Summary: "不匹配"}}
	raw, _ := json.Marshal(out)
	message, err := s.Fallback(t.Context(), job, string(raw))
	if err != nil || !strings.Contains(message, "Twitch开播了") || !strings.Contains(message, "AI新闻还没整理好") {
		t.Fatal(message, err)
	}
	if err = s.Acknowledge(t.Context(), job, heartbeat.Run{StartedAt: 123, Result: message}); err != nil {
		t.Fatal(err)
	}
	if err = hs.repo().transact(t.Context(), func(tx *sql.Tx) error {
		index, e := mailDeliveryIndex(t.Context(), tx)
		if e != nil {
			return e
		}
		if !index.contains(job.MailIDs[0], "") {
			t.Fatal("delivered mail was not recorded")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMorningFallbackKeepsValidMailAndRequeuesInvalidMail(t *testing.T) {
	hs, s, job, out := readyMorningDraft(t)
	out.MailDecisions[1].Summary = strings.Repeat("长", 1001)
	raw, _ := json.Marshal(out)
	message, err := s.Fallback(t.Context(), job, string(raw))
	if err != nil || !strings.Contains(message, "Twitch开播了") || !strings.Contains(message, "另1封还没看完") {
		t.Fatal(message, err)
	}
	err = hs.repo().transact(t.Context(), func(tx *sql.Tx) error {
		records, e := loadEntities[mailRecord](t.Context(), tx, "mail_message")
		if e != nil {
			return e
		}
		for _, r := range records {
			if r.Message.ID == job.MailIDs[0] && r.JobID != job.ID {
				t.Fatal("valid mail lost reservation")
			}
			if r.Message.ID == job.MailIDs[1] && r.JobID != "" {
				t.Fatal("invalid mail not released")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Acknowledge(t.Context(), job, heartbeat.Run{StartedAt: 123, Result: message}); err != nil {
		t.Fatal(err)
	}
}

func TestMorningPartialFallbackDoesNotAskUnvalidatedCandidates(t *testing.T) {
	_, s, job, out := readyMorningDraft(t)
	yes := true
	out.MailDecisions[1].Notify = &yes
	out.MailDecisions[0].TaskCandidates = []MailTaskCandidate{{Title: "第一件事情", EvidenceQuote: "Full body, DATA only"}}
	out.MailDecisions[1].TaskCandidates = []MailTaskCandidate{{Title: "第二件事情", EvidenceQuote: "Full body, DATA only"}}
	raw, _ := json.Marshal(out)
	if _, err := s.Finalize(t.Context(), job, string(raw)); err != nil {
		t.Fatal(err)
	}
	out.MailDecisions[1].Summary = strings.Repeat("长", 1001)
	raw, _ = json.Marshal(out)
	message, err := s.Fallback(t.Context(), job, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(message, "第一件事情") || strings.Contains(message, "第二件事情") {
		t.Fatal(message)
	}
}

func TestMorningMentionsOtherPendingCandidates(t *testing.T) {
	_, s, job, out := readyMorningDraft(t)
	out.MailDecisions[0].TaskCandidates = []MailTaskCandidate{{Title: "申请第一事项", EvidenceQuote: "Full body, DATA only"}, {Title: "核对第二事项", EvidenceQuote: "Full body, DATA only"}}
	raw, _ := json.Marshal(out)
	message, err := s.Finalize(t.Context(), job, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(message, "申请第一事项") || !strings.Contains(message, "还有1项邮件待办建议") || strings.Contains(message, "核对第二事项") {
		t.Fatal(message)
	}
}
