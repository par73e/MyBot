package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/tool"
)

func executeMemoryRequest(ctx context.Context, t *testing.T, home string, r MemoryRequest) (string, error) {
	t.Helper()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return personalMemoryTool(home, Options{}).Execute(ctx, raw)
}

func TestMemorySourceAcceptsClearPreferencesFactsAndDatedPlansWithoutExtraConfirmation(t *testing.T) {
	for _, tc := range []struct{ category, kind, quote, start, end string }{
		{"preference", "long", "以后回答先给结论", "", ""},
		{"personal_fact", "long", "我是计算机专业的学生", "", ""},
		{"temporary_plan", "short", "2099年1月1日至5日在杭州", "2099-01-01T00:00:00+08:00", "2099-01-06T00:00:00+08:00"},
	} {
		t.Run(tc.category, func(t *testing.T) {
			home := t.TempDir()
			ctx := tool.WithForegroundAuthorization(t.Context(), tool.ForegroundAuthorization{MessageID: "human-current", Text: tc.quote})
			got, err := executeMemoryRequest(ctx, t, home, MemoryRequest{Action: "create", Kind: tc.kind, Subject: "个人信息", Body: tc.quote, Category: tc.category, SourceQuote: tc.quote, StartsAt: tc.start, ExpiresAt: tc.end})
			if err != nil {
				t.Fatal(got, err)
			}
			all, err := (Memories{Home: home}).Apply(t.Context(), MemoryRequest{Action: "list"})
			if err != nil || len(all) != 1 || all[0].Body != tc.quote || all[0].SourceMessageID != "human-current" || all[0].Category != tc.category {
				t.Fatal(all, err)
			}
		})
	}
}

func TestMemorySourceRejectsEventExpansionHistoryAndMissingEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, text, quote, body, category string
		proof                             bool
	}{
		{"one_off_configuration", "这条就是我自己配置的。是在帮你做配置呀", "这条就是我自己配置的", "这条就是我自己配置的", "personal_fact", true},
		{"expanded_alert_rule", "这是我自己配置的", "这是我自己配置的", "这是我自己配置的，以后相关安全邮件不再提醒", "personal_fact", true},
		{"past_assistant_text", "知道了", "以后安全邮件静默", "以后安全邮件静默", "preference", true},
		{"missing_current_user", "以后先给结论", "以后先给结论", "以后先给结论", "preference", false},
		{"missing_quote", "以后先给结论", "", "以后先给结论", "preference", true},
		{"missing_category", "以后先给结论", "以后先给结论", "以后先给结论", "", true},
		{"test_progress", "测试成功了", "测试成功了", "测试成功了", "personal_fact", true},
		{"approval_progress", "我已经批准了", "我已经批准了", "我已经批准了", "personal_fact", true},
		{"negation_removed", "我不喜欢冗长回答", "喜欢冗长回答", "喜欢冗长回答", "preference", true},
		{"quoted_message", "> 我喜欢冗长回答\n这段说法有什么问题？", "我喜欢冗长回答", "我喜欢冗长回答", "preference", true},
		{"code_example", "```text\n我喜欢冗长回答\n```\n这是测试样例", "我喜欢冗长回答", "我喜欢冗长回答", "preference", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			ctx := t.Context()
			if tc.proof {
				ctx = tool.WithForegroundAuthorization(ctx, tool.ForegroundAuthorization{MessageID: "human-current", Text: tc.text, PreviousAssistant: "以后安全邮件静默"})
			}
			if got, err := executeMemoryRequest(ctx, t, home, MemoryRequest{Action: "create", Kind: "long", Subject: "测试", Body: tc.body, Category: tc.category, SourceQuote: tc.quote}); err == nil {
				t.Fatal("ineligible memory was saved", got)
			}
			all, err := (Memories{Home: home}).Apply(t.Context(), MemoryRequest{Action: "list"})
			if err != nil || len(all) != 0 {
				t.Fatal("rejected memory changed storage", all, err)
			}
		})
	}
}

func TestMemorySourceUpdateRequiresFreshQuoteAndPreservesArchiveAndRead(t *testing.T) {
	home := t.TempDir()
	saved, err := (Memories{Home: home}).Apply(t.Context(), MemoryRequest{Action: "create", Kind: "long", Subject: "表达偏好", Body: "先给结论"})
	if err != nil {
		t.Fatal(err)
	}
	id := saved[0].ID
	request := MemoryRequest{Action: "update", ID: id, Revision: 1, Body: "以后先解释背景", SourceQuote: "以后先解释背景", Category: "preference"}
	if _, err := executeMemoryRequest(t.Context(), t, home, request); err == nil {
		t.Fatal("update without current-user proof accepted")
	}
	ctx := tool.WithForegroundAuthorization(t.Context(), tool.ForegroundAuthorization{MessageID: "human-update", Text: request.Body})
	if _, err := executeMemoryRequest(ctx, t, home, request); err != nil {
		t.Fatal(err)
	}
	current, err := (Memories{Home: home}).Apply(t.Context(), MemoryRequest{Action: "get", ID: id})
	if err != nil || current[0].Revision != 2 || current[0].SourceMessageID != "human-update" || current[0].Body != request.Body {
		t.Fatal(current, err)
	}
	for _, action := range []string{"list", "get", "archive"} {
		if _, err := executeMemoryRequest(t.Context(), t, home, MemoryRequest{Action: action, ID: id, Revision: 2}); err != nil {
			t.Fatal(action, err)
		}
	}
	if got, err := executeMemoryRequest(t.Context(), t, home, MemoryRequest{Action: "list"}); err != nil || strings.Contains(got, id) {
		t.Fatal("archive was not effective", got, err)
	}
}

func TestMemorySourceTemporaryPlanCannotBeStoredAsLongOnUpdate(t *testing.T) {
	home := t.TempDir()
	saved, err := (Memories{Home: home}).Apply(t.Context(), MemoryRequest{Action: "create", Kind: "long", Subject: "地点", Body: "在上海"})
	if err != nil {
		t.Fatal(err)
	}
	quote := "2099年1月1日至5日在杭州"
	ctx := tool.WithForegroundAuthorization(t.Context(), tool.ForegroundAuthorization{MessageID: "human-plan", Text: quote})
	_, err = executeMemoryRequest(ctx, t, home, MemoryRequest{Action: "update", ID: saved[0].ID, Revision: 1, Body: quote, Category: "temporary_plan", SourceQuote: quote})
	if err == nil {
		t.Fatal("temporary plan changed an existing long memory without short boundaries")
	}
}
