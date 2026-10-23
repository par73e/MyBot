package bot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

type finalOnlyAdapter struct{ *fakeAdapter }

func (*finalOnlyAdapter) FinalOnlyReplies() bool { return true }

func newFinalSink(t *testing.T) (*renderSink, *fakeAdapter) {
	t.Helper()
	a := newFakeAdapter(PlatformWeixin, "final-test")
	s := newRenderSink(t.Context(), &finalOnlyAdapter{a}, "weixin", "weixin", "chat", ChatDM, "owner", "inbound", slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	return s, a
}

func TestFinalOnlySuppressesIntermediateEventsAndKeepsLastCommittedAnswer(t *testing.T) {
	s, a := newFinalSink(t)
	s.Emit(event.Event{Kind: event.TurnStarted})
	for _, e := range []event.Event{
		{Kind: event.Reasoning, Text: "private reasoning"},
		{Kind: event.Text, Text: "我先查询。"},
		{Kind: event.Message, Text: "我先查询。"},
		{Kind: event.ToolDispatch, Tool: event.Tool{ID: "t1", Name: "use_capability"}},
		{Kind: event.ToolProgress, Tool: event.Tool{Output: "tool output"}},
		{Kind: event.ToolResult, Tool: event.Tool{ID: "t1", Err: "temporary failure"}},
		{Kind: event.Notice, Level: event.LevelWarn, Text: "retrying"},
		{Kind: event.CompactionStarted},
		{Kind: event.Message, Text: "准备继续。"},
		{Kind: event.StreamAttempt, StreamAttempt: event.StreamAttemptInfo{Action: event.StreamAttemptBegin}},
		{Kind: event.Text, Text: "failed attempt"},
		{Kind: event.StreamAttempt, StreamAttempt: event.StreamAttemptInfo{Action: event.StreamAttemptDiscard}},
		{Kind: event.Text, Text: "raw final deltas"},
		{Kind: event.Message, Text: "Sir，今晚预计有雨。"},
	} {
		s.Emit(e)
	}
	if got := a.sentMessages(); len(got) != 0 {
		t.Fatalf("premature messages: %+v", got)
	}
	s.Emit(event.Event{Kind: event.TurnDone})
	s.Emit(event.Event{Kind: event.TurnDone})
	got := a.sentMessages()
	if len(got) != 1 || got[0].Text != "Sir，今晚预计有雨。" {
		t.Fatalf("expected only accepted final answer once: %+v", got)
	}
}

func TestFinalOnlyDoesNotSendIncompleteAnswers(t *testing.T) {
	for _, tc := range []struct {
		name         string
		events       []event.Event
		wantMessages int
	}{
		{"failure", []event.Event{{Kind: event.Message, Text: "unfinished"}, {Kind: event.TurnDone, Err: errors.New("private backend error")}}, 1},
		{"cancel", []event.Event{{Kind: event.Message, Text: "unfinished"}, {Kind: event.TurnDone, Err: context.Canceled}}, 0},
		{"cancelled receipt", []event.Event{{Kind: event.Message, Text: "unfinished"}, {Kind: event.TurnDone, Cancelled: true}}, 0},
		{"reasoning only", []event.Event{{Kind: event.Message, Reasoning: "private reasoning"}, {Kind: event.TurnDone}}, 1},
		{"uncommitted delta", []event.Event{{Kind: event.Text, Text: "unfinished"}, {Kind: event.TurnDone}}, 1},
		{"tool round without dispatch", []event.Event{{Kind: event.Message, Text: "unfinished"}, {Kind: event.StreamAttempt, StreamAttempt: event.StreamAttemptInfo{Action: event.StreamAttemptCommit, Response: &event.ResponseSummary{ToolCalls: 1, TextBytes: 10, StreamComplete: true}}}, {Kind: event.TurnDone}}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, a := newFinalSink(t)
			for _, e := range tc.events {
				s.Emit(e)
			}
			got := a.sentMessages()
			if len(got) != tc.wantMessages {
				t.Fatalf("messages: %+v", got)
			}
			for _, m := range got {
				if strings.Contains(m.Text, "private") || strings.Contains(m.Text, "unfinished") || !strings.Contains(m.Text, "未能生成完整答复") {
					t.Fatalf("invalid failure result: %q", m.Text)
				}
			}
		})
	}
}

func TestFinalOnlyKeepsInteractionsAndResetsBetweenTurns(t *testing.T) {
	s, a := newFinalSink(t)
	var approved, asked bool
	s.onApproval = func(event.Approval) { approved = true }
	s.onAsk = func(event.Ask) { asked = true }
	s.Emit(event.Event{Kind: event.ApprovalRequest, Approval: event.Approval{ID: "a1", Tool: "write_file", Subject: "test.txt"}})
	s.Emit(event.Event{Kind: event.AskRequest, Ask: event.Ask{ID: "q1", Questions: []event.AskQuestion{{ID: "city", Prompt: "哪个城市？"}}}})
	if !approved || !asked || len(a.sentMessages()) != 2 {
		t.Fatal("required interactions suppressed")
	}
	for _, text := range []string{"第一轮", "第二轮"} {
		s.Emit(event.Event{Kind: event.TurnStarted})
		s.Emit(event.Event{Kind: event.Message, Text: text})
		s.Emit(event.Event{Kind: event.TurnDone})
	}
	got := a.sentMessages()
	if len(got) != 4 || got[2].Text != "第一轮" || got[3].Text != "第二轮" {
		t.Fatalf("turn state leaked: %+v", got)
	}
}

func TestFinalOnlyLongAnswerUsesExistingLengthLimits(t *testing.T) {
	s, a := newFinalSink(t)
	want := strings.Repeat("长", renderMaxChunkRunes*2+10)
	s.Emit(event.Event{Kind: event.Message, Text: want})
	s.Emit(event.Event{Kind: event.TurnDone})
	var joined strings.Builder
	for _, msg := range a.sentMessages() {
		if len([]rune(msg.Text)) > renderMaxChunkRunes {
			t.Fatal("platform chunk limit exceeded")
		}
		joined.WriteString(msg.Text)
	}
	if joined.String() != want {
		t.Fatal("final answer lost or duplicated text")
	}
}

type finalTestTool struct{ calls int }

func (*finalTestTool) Name() string        { return "test_weather" }
func (*finalTestTool) Description() string { return "Read test weather." }
func (*finalTestTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (*finalTestTool) ReadOnly() bool { return true }
func (t *finalTestTool) Execute(context.Context, json.RawMessage) (string, error) {
	t.calls++
	return "rain", nil
}

func TestFinalOnlyRealAgentToolLoop(t *testing.T) {
	s, a := newFinalSink(t)
	registry := tool.NewRegistry()
	weather := &finalTestTool{}
	registry.Add(weather)
	p := testutil.NewMock("test-model",
		testutil.Turn{Text: "我先查天气。", ToolCalls: []provider.ToolCall{{ID: "w1", Name: "test_weather", Arguments: "{}"}}},
		testutil.Turn{Text: "再核对一次。", ToolCalls: []provider.ToolCall{{ID: "w2", Name: "test_weather", Arguments: "{}"}}},
		testutil.Turn{Text: "Sir，今天有雨，请带伞。"},
	)
	exec := agent.New(p, registry, agent.NewSession("test"), agent.Options{MaxSteps: 6}, s)
	if err := exec.Run(t.Context(), "查天气"); err != nil {
		t.Fatal(err)
	}
	if got := a.sentMessages(); len(got) != 0 {
		t.Fatalf("sent before outer turn finished: %+v", got)
	}
	s.Emit(event.Event{Kind: event.TurnDone})
	got := a.sentMessages()
	if weather.calls != 2 || len(got) != 1 || got[0].Text != "Sir，今天有雨，请带伞。" {
		t.Fatalf("tool calls=%d messages=%+v", weather.calls, got)
	}
}
