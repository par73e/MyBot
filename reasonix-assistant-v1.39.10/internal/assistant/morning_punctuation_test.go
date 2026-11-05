package assistant

import (
	"strings"
	"testing"
)

func TestMorningSummarySeparatorsDoNotDuplicateTerminalPunctuation(t *testing.T) {
	for _, ending := range []string{"。", "；", ";", ".", "！", "?", "；。;  "} {
		t.Run(ending, func(t *testing.T) {
			notify := true
			snap := MorningSnapshot{MailStatus: "ok", CourseStatus: "ok"}
			out := MorningOutput{
				News: []MorningNews{
					{Summary: "OpenAI将收购Pierre，扩展Codex" + ending},
					{Summary: "博主讨论AI广告骗局" + ending},
					{Summary: "IEEE讨论AI技术债" + ending},
				},
				MailDecisions: []MailDecision{
					{Notify: &notify, Summary: "音乐订阅已续费" + ending},
					{Notify: &notify, Summary: "快递今天送达" + ending},
				},
			}
			lines := strings.Split(renderMorning(snap, out), "\n")
			if len(lines) != 6 {
				t.Fatalf("morning layout changed: %q", lines)
			}
			if want := "AI圈有3条消息，OpenAI将收购Pierre，扩展Codex；博主讨论AI广告骗局；IEEE讨论AI技术债。"; lines[5] != want {
				t.Errorf("news = %q, want %q", lines[5], want)
			}
			if want := "收到的新邮件里，音乐订阅已续费；快递今天送达。"; lines[3] != want {
				t.Errorf("mail = %q, want %q", lines[3], want)
			}
		})
	}
}

func TestMorningSummaryKeepsMeaningfulInternalPunctuation(t *testing.T) {
	out := MorningOutput{News: []MorningNews{
		{Summary: "模型v1.2发布，支持工具调用。"},
		{Summary: "研究称有改善；结果仍待验证。"},
	}}
	lines := strings.Split(renderMorning(MorningSnapshot{MailStatus: "ok"}, out), "\n")
	if want := "AI圈有2条消息，模型v1.2发布，支持工具调用；研究称有改善；结果仍待验证。"; lines[5] != want {
		t.Fatalf("news = %q, want %q", lines[5], want)
	}
}
