package assistant

import (
	"context"
	"encoding/json"
	"reasonix/internal/heartbeat"
	"reasonix/internal/tool"
	"strings"
	"testing"
	"time"
)

func proposalFixture(t *testing.T) (Proposals, MailService, heartbeat.Task, string) {
	t.Helper()
	mail, options := mailFixture(t)
	h := HeartbeatService{Home: mail.Home, Options: options}
	if _, e := h.EnableExpansion(t.Context()); e != nil {
		t.Fatal(e)
	}
	quote := "请于2026年10月27日18:00前提交报名材料"
	body := quote + "。\n" + strings.Repeat("普通邮件内容。", 1200)
	mail.Transport = func(_ context.Context, r map[string]any) (mailWire, error) {
		if r["action"] == "read" {
			return mailWire{Results: []mailResult{{Account: "qq", OK: true, Body: body}}}, nil
		}
		return mailWire{Results: []mailResult{goodResult("qq", MailMessage{TransportID: "1", MessageID: "<proposal-fixture>", Subject: "报名通知"}), goodResult("gmail1"), goodResult("gmail2")}}, nil
	}
	if _, e := mail.Poll(t.Context(), options); e != nil {
		t.Fatal(e)
	}
	jobs := jobsForMail(t, mail)
	job := jobs[0]
	job.RunStartedAt = 123
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "1")
	t.Setenv("REASONIX_ASSISTANT_MAIL_BACKGROUND", "1")
	t.Setenv("REASONIX_ASSISTANT_JOB_ID", job.ID)
	t.Setenv("REASONIX_ASSISTANT_MAIL_ATTEMPT", "123")
	return Proposals{Home: mail.Home, Options: options, Clock: func() time.Time { return timeMust("2026-10-25T12:00:00+08:00") }}, mail, job, quote
}
func recordProposal(t *testing.T, mail MailService, job heartbeat.Task, quote string) error {
	t.Helper()
	yes := true
	raw, _ := json.Marshal(map[string]any{"notify": true, "mail_decisions": []MailDecision{{ID: job.MailIDs[0], Notify: &yes, Summary: "报名材料待提交", Reason: "需要行动", TaskCandidates: []MailTaskCandidate{{Title: "提交报名材料", DueAt: "2026-10-27T18:00:00+08:00", EvidenceQuote: quote}}}}})
	return mail.RecordMailDecisions(t.Context(), job, string(raw))
}
func TestMailProposalRequiresFullReadAndRealConfirmation(t *testing.T) {
	svc, mail, job, quote := proposalFixture(t)
	page, e := mail.Query(t.Context(), MailRequest{Action: "read", ID: job.MailIDs[0]})
	if e != nil {
		t.Fatal(e)
	}
	if e = recordProposal(t, mail, job, quote); e == nil {
		t.Fatal("partial body created a proposal")
	}
	for page.(map[string]any)["has_more"].(bool) {
		page, e = mail.Query(t.Context(), MailRequest{Action: "read", ID: job.MailIDs[0], BodyOffset: page.(map[string]any)["next_body_offset"].(int)})
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = recordProposal(t, mail, job, "臆造的截止证据"); e == nil {
		t.Fatal("fabricated evidence accepted")
	}
	if e = recordProposal(t, mail, job, quote); e != nil {
		t.Fatal(e)
	}
	todos := Tasks{Repository: mail.Repository}
	all, e := todos.List(t.Context(), true)
	if e != nil || len(all) != 0 {
		t.Fatal("proposal wrote formal task", all, e)
	}
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "")
	t.Setenv("REASONIX_ASSISTANT_MAIL_BACKGROUND", "")
	out, e := svc.Apply(t.Context(), ProposalRequest{Action: "list"})
	if e != nil || len(out.([]TaskProposal)) != 0 {
		t.Fatal("unfinished analysis leaked", out, e)
	}
	if e = (JobStore{Repository: mail.Repository}).Update(t.Context(), job.ID, func(j *heartbeat.Task) error {
		j.History = []heartbeat.Run{{StartedAt: 123, Status: "delivered"}}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	out, e = svc.Apply(t.Context(), ProposalRequest{Action: "list"})
	if e != nil || len(out.([]TaskProposal)) != 1 {
		t.Fatal(out, e)
	}
	p := out.([]TaskProposal)[0]
	req := ProposalRequest{Action: "approve", ID: p.ID, Revision: p.Revision}
	if _, e = svc.Apply(t.Context(), req); e == nil {
		t.Fatal("model-only authorization accepted")
	}
	ctx := tool.WithForegroundAuthorization(t.Context(), tool.ForegroundAuthorization{MessageID: "human-1", Text: "你好吗", PreviousAssistant: p.Question})
	if _, e = svc.Apply(ctx, req); e == nil {
		t.Fatal("unrelated greeting accepted")
	}
	ctx = tool.WithForegroundAuthorization(t.Context(), tool.ForegroundAuthorization{MessageID: "human-2", Text: "可以，帮我记下", PreviousAssistant: p.Question})
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "1")
	if _, e = svc.Apply(ctx, req); e == nil {
		t.Fatal("background mutated task")
	}
	t.Setenv("REASONIX_ASSISTANT_BACKGROUND", "")
	if _, e = svc.Apply(ctx, req); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Apply(ctx, req); e != nil {
		t.Fatal("approval replay not idempotent", e)
	}
	all, e = todos.List(t.Context(), true)
	if e != nil || len(all) != 1 || len(all[0].Reminders) != 2 || all[0].SourceProposal != p.ID {
		t.Fatal(all, e)
	}
	if e = recordProposal(t, mail, job, quote); e != nil {
		t.Fatal(e)
	}
	out, e = svc.Apply(t.Context(), ProposalRequest{Action: "list"})
	if e != nil || len(out.([]TaskProposal)) != 0 {
		t.Fatal("duplicate reasked", out, e)
	}
}
