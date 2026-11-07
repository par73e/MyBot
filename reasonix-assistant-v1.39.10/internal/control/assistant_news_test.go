package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/assistant"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

type newsCompletionRunner struct {
	session  *agent.Session
	messages []provider.Message
	err      error
	cancel   context.CancelFunc
}

func (r newsCompletionRunner) Run(ctx context.Context, input string) error {
	r.session.Add(provider.Message{Role: provider.RoleUser, Content: input})
	for _, m := range r.messages {
		r.session.Add(m)
	}
	if r.cancel != nil {
		r.cancel()
	}
	return r.err
}
func TestNewsIndexedOnlyAfterSuccessfulForegroundTurn(t *testing.T) {
	for _, mode := range []string{"headless", "orchestrated"} {
		for _, outcome := range []string{"success", "error", "cancelled"} {
			t.Run(mode+"/"+outcome, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("REASONIX_HOME", home)
				t.Setenv("REASONIX_ASSISTANT", "1")
				t.Setenv("ASROBOT_FOREGROUND_SOURCE", "web")
				t.Setenv("REASONIX_ASSISTANT_NEWS_BACKGROUND", "")
				now := time.Now()
				service := assistant.HotNewsService{Home: home, Clock: func() time.Time { return now }, Fetch: func(context.Context, string) ([]byte, error) {
					return []byte(fmt.Sprintf(`<rss><channel><title>AI Source</title><item><title>AI model released</title><link>https://example.com/ai</link><pubDate>%s</pubDate><description>Official AI model evaluation results with verified context and caveats.</description></item></channel></rss>`, now.Add(-time.Hour).Format(time.RFC1123Z))), nil
				}}
				news := assistant.NewsTool{Service: service}
				raw, err := news.Execute(t.Context(), json.RawMessage(`{"action":"list"}`))
				if err != nil {
					t.Fatal(err)
				}
				var snapshot assistant.HotNewsSnapshot
				if err = json.Unmarshal([]byte(raw), &snapshot); err != nil || len(snapshot.Candidates) == 0 {
					t.Fatal(raw, err)
				}
				var messages []provider.Message
				pair := func(id string, args any) {
					raw, _ := json.Marshal(args)
					result, err := news.Execute(t.Context(), raw)
					if err != nil {
						t.Fatal(err)
					}
					messages = append(messages, provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: id, Name: "assistant_ai_news", Arguments: string(raw)}}}, provider.Message{Role: provider.RoleTool, ToolCallID: id, Content: result})
				}
				pair("read", map[string]any{"action": "read", "snapshot_id": snapshot.ID, "ids": []string{snapshot.Candidates[0].ID}})
				pair("check", map[string]any{"action": "check", "events": []assistant.NewsEventSummary{{IDs: []string{snapshot.Candidates[0].ID}, Title: "模型发布", Summary: "团队公布了评测结果。"}}})
				messages = append(messages, provider.Message{Role: provider.RoleAssistant, Content: "# **AI News**\n\n**1. 模型发布**\n团队公布了评测结果。\n[原文](https://example.com/ai)"})
				sess := agent.NewSession("sys")
				exec := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				runner := newsCompletionRunner{session: sess, messages: messages}
				if outcome == "error" {
					runner.err = errors.New("provider failed")
				}
				if outcome == "cancelled" {
					runner.cancel = cancel
				}
				c := newOwnedTestController(t, Options{Executor: exec, Runner: runner})
				if mode == "headless" {
					err = c.runReady(ctx, "今日新闻")
				} else {
					err = newTurnOrchestrator(c).runTurnWithRawDisplay(ctx, "今日新闻", "今日新闻", "")
				}
				if outcome == "success" && err != nil {
					t.Fatal(err)
				}
				latest, err := news.Execute(t.Context(), json.RawMessage(`{"action":"latest"}`))
				if err != nil {
					t.Fatal(err)
				}
				if outcome == "success" {
					if !strings.Contains(latest, `"source":"web"`) {
						t.Fatal(latest)
					}
				} else if latest != `{"available":false}` {
					t.Fatal("incomplete turn replaced report:", latest)
				}
			})
		}
	}
}
