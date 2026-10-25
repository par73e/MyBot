package assistant

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNewsRecentDoesNotPretendPopularityOrFillOldItems(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-10-25T08:00:00+08:00")
	s := NewsService{Fetch: func(_ context.Context, path string) (string, error) {
		if path == "/articles-category/ai" {
			return `<a href="/articles/new">new</a><a href="/articles/old">old</a>`, nil
		}
		at := now.Add(-time.Hour)
		if strings.HasSuffix(path, "old") {
			at = now.Add(-25 * time.Hour)
		}
		return fmt.Sprintf(`<meta property="article:published_time" content="%s"><h6>AI RESEARCH</h6><h4><a href="https://example.com/news">A new result</a></h4><p>Evidence with limitations.</p><h6>PRESENTED BY SPONSOR</h6><h4><a href="https://example.com/ad">Ad</a></h4><p>Buy it</p>`, at.Format(time.RFC3339)), nil
	}}
	p := s.Recent(t.Context(), now)
	if len(p.Items) != 1 || p.Status != "ok" || p.Ranking != "editorial_selection_not_popularity" || !strings.Contains(p.Items[0].Text, "Evidence") || strings.Contains(p.Items[0].Text, "Buy it") {
		t.Fatal(p)
	}
	s.Fetch = func(context.Context, string) (string, error) { return "", fmt.Errorf("offline") }
	p = s.Recent(t.Context(), now)
	if p.Status != "unavailable" || len(p.Items) != 0 {
		t.Fatal(p)
	}
}
