package bot

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var renderURLPattern = regexp.MustCompile(`(?:\[[^\]\r\n]*\]\()?https?://[^\s<>]+`)

// A length fallback can land inside a URL even when semantic boundaries ignore
// URL punctuation. Move the URL to the next chunk, or send it intact when it is
// already the first token. Pathological URLs larger than the hard cap retain
// the existing length fallback so flushing always makes bounded progress.
func urlSafeChunkIndex(text string, idx int) int {
	for _, span := range renderURLPattern.FindAllStringIndex(text, -1) {
		if idx <= span[0] || idx >= span[1] {
			continue
		}
		if strings.TrimSpace(text[:span[0]]) != "" {
			return span[0]
		}
		if utf8.RuneCountInString(text[span[0]:span[1]]) <= renderHardChunkRunes {
			return span[1]
		}
	}
	return idx
}
