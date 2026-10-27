package assistant

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"reasonix/internal/tool"
)

// Source grounding is a native boundary. Deciding whether a personal statement
// has lasting value remains the foreground model's task under the shared policy.
func authorizeMemoryWrite(ctx context.Context, r *MemoryRequest) error {
	if r.Action != "create" && r.Action != "update" {
		return nil
	}
	a, ok := tool.ForegroundAuthorizationFrom(ctx)
	if !ok {
		return errors.New("memory not saved: create/update requires a real current foreground user message")
	}
	quote := strings.TrimSpace(r.SourceQuote)
	if quote == "" || len(quote) > 6000 || !strings.Contains(a.Text, quote) || !groundedMemoryQuote(a.Text, quote) {
		return errors.New("memory not saved: source_quote must exactly quote the current user message, not history, tools or assistant text; do not ask whether to save incidental remarks")
	}
	switch r.Category {
	case "preference", "personal_fact", "temporary_plan":
	default:
		return errors.New("memory not saved: category must be preference, personal_fact or temporary_plan; event confirmations and configuration state are not personal memory")
	}
	if memoryEventConfirmation(quote) {
		return errors.New("memory not saved: this is an event confirmation or conversation progress, not a lasting personal statement; answer normally without a memory confirmation question")
	}
	if r.Action == "create" || r.Body != "" {
		if strings.TrimSpace(r.Body) != quote {
			return errors.New("memory not saved: body must equal source_quote verbatim; never expand a fact into a future behavior or notification rule")
		}
	}
	if r.Category == "temporary_plan" && (r.Kind == "long" || r.Action == "create" && r.Kind != "short") {
		return errors.New("memory not saved: a temporary plan requires kind=short with explicit starts_at/expires_at")
	}
	r.SourceQuote = quote
	r.sourceMessageID = a.MessageID
	return nil
}

// Keep whole clauses so a substring cannot drop a negation or turn a quoted
// code/example block into the owner's direct personal statement.
func groundedMemoryQuote(text, quote string) bool {
	var lines []string
	fence := ""
	for line := range strings.SplitSeq(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if fence == "" {
				fence = marker
			} else if fence == marker {
				fence = ""
			}
			continue
		}
		if fence == "" && !strings.HasPrefix(trimmed, ">") {
			lines = append(lines, line)
		}
	}
	text = strings.Join(lines, "\n")
	boundary := func(r rune) bool { return strings.ContainsRune("\n\r。！？!?；;，,：:.", r) }
	for offset := 0; offset <= len(text)-len(quote); {
		i := strings.Index(text[offset:], quote)
		if i < 0 {
			return false
		}
		i += offset
		before := strings.TrimRight(text[:i], " \t")
		after := strings.TrimLeft(text[i+len(quote):], " \t")
		left, _ := utf8.DecodeLastRuneInString(before)
		right, _ := utf8.DecodeRuneInString(after)
		end, _ := utf8.DecodeLastRuneInString(quote)
		if (before == "" || boundary(left)) && (after == "" || boundary(right) || boundary(end)) {
			return true
		}
		offset = i + len(quote)
	}
	return false
}

func memoryEventConfirmation(quote string) bool {
	text := strings.ToLower(strings.Trim(quote, " \t\r\n。！!，,."))
	for _, ack := range []string{"知道了", "明白了", "好的", "谢谢", "ok", "测试成功了", "测试通过了", "我已经批准了", "已经批准了"} {
		if text == ack {
			return true
		}
	}
	if !strings.HasPrefix(text, "这条") && !strings.HasPrefix(text, "这个") && !strings.HasPrefix(text, "这次") {
		return false
	}
	if !strings.Contains(text, "我自己") && !strings.Contains(text, "我本人") && !strings.Contains(text, "是我") {
		return false
	}
	for _, action := range []string{"配置", "设置", "创建", "操作", "批准"} {
		if strings.Contains(text, action) {
			return true
		}
	}
	return false
}
