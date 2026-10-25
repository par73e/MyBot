package bot

import (
	"reasonix/internal/event"
	"strings"
	"testing"
)

func TestFinalReplyKeepsShortNewsAndTrailingURLTogether(t *testing.T) {
	link := "https://www.forbes.com/sites/thomasbrewster/2026/10/23/inside-openais-plan-to-protect-the-internet-before-hackers-break-it"
	for _, tail := range []string{link, "[原文](" + link + ")"} {
		s, a := newFinalSink(t)
		want := "Sir，AI News\n\n**7. Forbes报道OpenAI破晓计划**\n报道称向伙伴开放安全模型。\n" + tail
		s.Emit(event.Event{Kind: event.Message, Text: want})
		s.Emit(event.Event{Kind: event.TurnDone})
		s.Emit(event.Event{Kind: event.TurnDone})
		got := a.sentMessages()
		if len(got) != 1 || got[0].Text != want {
			t.Fatalf("short answer split or changed: %+v", got)
		}
	}
}
func TestURLPunctuationIsNotASemanticBoundary(t *testing.T) {
	prefix := "报道如下。 "
	text := prefix + "https://www.example.com/read?v=1;a=2!#section"
	if got := lastSemanticBoundary(text, len([]rune(text))); got != len(prefix) {
		t.Fatalf("split inside URL at %d: %q", got, text[:got])
	}
}
func TestLongFinalReplyKeepsWholeURLsAndAllText(t *testing.T) {
	link := "https://www.forbes.com/sites/thomasbrewster/2026/10/23/inside-openais-plan-to-protect-the-internet-before-hackers-break-it"
	for _, prefix := range []string{strings.Repeat("长", renderMaxChunkRunes-20), strings.Repeat("句。", 1000), strings.Repeat("长", renderMaxChunkRunes-20) + "[原文]("} {
		s, a := newFinalSink(t)
		want := prefix + link
		s.Emit(event.Event{Kind: event.Message, Text: want})
		s.Emit(event.Event{Kind: event.TurnDone})
		var joined strings.Builder
		found := 0
		for _, msg := range a.sentMessages() {
			joined.WriteString(msg.Text)
			if strings.Contains(msg.Text, link) {
				found++
			}
			if len([]rune(msg.Text)) > renderHardChunkRunes {
				t.Fatal("hard chunk limit exceeded")
			}
		}
		if joined.String() != want || found != 1 {
			t.Fatalf("URL split, lost or duplicated: %+v", a.sentMessages())
		}
	}
}
func TestOversizeURLStillMakesBoundedProgress(t *testing.T) {
	s, a := newFinalSink(t)
	want := "https://example.com/" + strings.Repeat("x", renderHardChunkRunes+10)
	s.Emit(event.Event{Kind: event.Message, Text: want})
	s.Emit(event.Event{Kind: event.TurnDone})
	var joined strings.Builder
	for _, msg := range a.sentMessages() {
		joined.WriteString(msg.Text)
		if len([]rune(msg.Text)) > renderHardChunkRunes {
			t.Fatal("unbounded URL chunk")
		}
	}
	if joined.String() != want {
		t.Fatal("oversize URL lost data")
	}
}
