package heartbeat

import (
	"context"
	"errors"
	"testing"
)

type conversationRunner struct{ task Task }

func (r *conversationRunner) Run(_ context.Context, t Task) (Execution, error) {
	r.task = t
	return Execution{Text: "report", SessionID: "execution", ConversationID: "daily"}, nil
}

type conversationSender struct{ run Run }

func (*conversationSender) Send(context.Context, Target, string) error {
	return errors.New("lost run-aware delivery")
}
func (s *conversationSender) SendRun(_ context.Context, _ Task, run Run) error {
	s.run = run
	return nil
}

func TestConversationRunKeepsAdmissionTimeAndSeparateExecutionIdentity(t *testing.T) {
	store, task, now := fixture(t)
	if err := store.Update(t.Context(), task.ID, func(t *Task) error {
		t.Target = &Target{ConnectionID: "weixin", ChatID: "owner"}
		t.History = []Run{{Status: "completed", SessionID: "previous-execution"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runner, sender := &conversationRunner{}, &conversationSender{}
	if err := (Engine{Store: store, Runner: runner, Sender: sender}).Tick(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if runner.task.RunStartedAt != now.UnixMilli() || len(runner.task.History) != 1 || runner.task.History[0].SessionID != "previous-execution" {
		t.Fatal("claim time or previous execution history lost")
	}
	if sender.run.StartedAt != now.UnixMilli() || sender.run.SessionID != "execution" || sender.run.ConversationID != "daily" {
		t.Fatal("delivery confused background execution with day conversation")
	}
	saved := onlyTask(t, store)
	last := lastRun(&saved)
	if last.Status != "delivered" || last.ConversationID != "daily" {
		t.Fatal("day identity was not persisted")
	}
}
