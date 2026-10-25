package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type hotNewsSource struct{ ID, URL string }

var hotRSSSources = []hotNewsSource{
	{"techmeme", "https://www.techmeme.com/feed.xml"},
	{"rundown", "https://rss.beehiiv.com/feeds/2R3C6Bt5wj.xml"},
	{"openai", "https://openai.com/news/rss.xml"},
	{"deepmind", "https://deepmind.google/blog/rss.xml"},
	{"qbitai", "https://www.qbitai.com/feed/"},
	{"decoder", "https://the-decoder.com/feed/"},
}

func (s HotNewsService) collect(ctx context.Context, now time.Time) HotNewsSnapshot {
	ctx, cancel := context.WithTimeout(ctx, 28*time.Second)
	defer cancel()
	snap := HotNewsSnapshot{Since: now.Add(-24 * time.Hour), AsOf: now, Status: "ok", Ranking: "source_consensus_and_hn_discussion_v1", Candidates: []HotNewsArticle{}, Sources: []NewsSourceState{}, TargetCount: 7, Instructions: NewsBriefingInstructions}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, source := range hotRSSSources {
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				snap.Sources = append(snap.Sources, NewsSourceState{ID: source.ID, URL: source.URL, Status: "unavailable", Error: "collection deadline"})
				mu.Unlock()
				return
			}
			defer func() { <-sem }()
			raw, e := s.fetchPublic(ctx, source.URL)
			state := NewsSourceState{ID: source.ID, URL: source.URL, Status: "ok"}
			var articles []HotNewsArticle
			if e == nil {
				articles, state.Items, e = parseHotRSS(raw, source.ID, now)
			}
			if e != nil {
				state.Status = "unavailable"
				state.Error = boundedNewsText(e.Error(), 180)
			}
			state.Recent = len(articles)
			mu.Lock()
			snap.Sources = append(snap.Sources, state)
			snap.Candidates = append(snap.Candidates, articles...)
			mu.Unlock()
		})
	}
	for _, query := range []string{"AI", "LLM", "OpenAI", "Anthropic", "Gemini", "DeepSeek"} {
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				snap.Sources = append(snap.Sources, NewsSourceState{ID: "hn_" + query, Status: "unavailable", Error: "collection deadline"})
				mu.Unlock()
				return
			}
			defer func() { <-sem }()
			articles, state := s.collectHN(ctx, query, now)
			mu.Lock()
			snap.Sources = append(snap.Sources, state)
			snap.Candidates = append(snap.Candidates, articles...)
			mu.Unlock()
		})
	}
	wg.Wait()
	snap.Candidates = rankHotNews(snap.Candidates, now)
	failures := 0
	for _, v := range snap.Sources {
		if v.Status != "ok" {
			failures++
		}
	}
	if failures > 0 {
		snap.Status = "partial"
	}
	if failures == len(snap.Sources) {
		snap.Status = "unavailable"
	}
	sort.Slice(snap.Sources, func(i, j int) bool { return snap.Sources[i].ID < snap.Sources[j].ID })
	return snap
}
func (s HotNewsService) collectHN(ctx context.Context, query string, now time.Time) ([]HotNewsArticle, NewsSourceState) {
	endpoint := "https://hn.algolia.com/api/v1/search?query=" + url.QueryEscape(query) + "&tags=story&numericFilters=created_at_i%3E%3D" + fmt.Sprint(now.Add(-24*time.Hour).Unix()) + "&hitsPerPage=20"
	state := NewsSourceState{ID: "hn_" + query, URL: endpoint, Status: "ok"}
	out := []HotNewsArticle{}
	raw, e := s.fetchPublic(ctx, endpoint)
	var response struct {
		Hits []struct {
			ID       string    `json:"objectID"`
			Title    string    `json:"title"`
			URL      string    `json:"url"`
			At       time.Time `json:"created_at"`
			Points   int       `json:"points"`
			Comments int       `json:"num_comments"`
		} `json:"hits"`
	}
	if e == nil {
		e = json.Unmarshal(raw, &response)
		if e == nil && response.Hits == nil {
			e = fmt.Errorf("HN search result structure missing")
		}
	}
	if e != nil {
		state.Status = "unavailable"
		state.Error = boundedNewsText(e.Error(), 180)
		return out, state
	}
	state.Items = len(response.Hits)
	for _, v := range response.Hits {
		u := canonicalNewsURL(v.URL)
		if u == "" || !newsRelevant(v.Title) || v.At.Before(now.Add(-24*time.Hour)) || v.At.After(now) {
			continue
		}
		out = append(out, HotNewsArticle{ID: newsID(u), Title: v.Title, URL: u, EvidenceURL: "https://news.ycombinator.com/item?id=" + v.ID, Sources: []string{"hackernews"}, PublishedAt: v.At, TimeBasis: "discussion_submission", Points: v.Points, Comments: v.Comments, HNID: v.ID, MetricAt: now, ContentStatus: "headline_only"})
	}
	state.Recent = len(out)
	return out, state
}
func rankHotNews(all []HotNewsArticle, now time.Time) []HotNewsArticle {
	byURL := map[string]HotNewsArticle{}
	for _, a := range all {
		if old, ok := byURL[a.URL]; ok {
			a = mergeHotNews(old, a)
		}
		byURL[a.URL] = a
	}
	out := make([]HotNewsArticle, 0, len(byURL))
	for _, a := range byURL {
		hours := math.Max(0, now.Sub(a.PublishedAt).Hours())
		a.RankScore = (math.Log1p(float64(a.Points))+0.45*math.Log1p(float64(a.Comments)))/math.Pow(hours+2, 0.55) + 0.6*float64(len(a.Sources)-1)
		if a.ContentStatus != "headline_only" {
			a.RankScore += 0.15
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RankScore != out[j].RankScore {
			return out[i].RankScore > out[j].RankScore
		}
		if !out[i].PublishedAt.Equal(out[j].PublishedAt) {
			return out[i].PublishedAt.After(out[j].PublishedAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > 28 {
		out = out[:28]
	}
	return out
}
func mergeHotNews(a, b HotNewsArticle) HotNewsArticle {
	a.Points = max(a.Points, b.Points)
	a.Comments = max(a.Comments, b.Comments)
	if a.HNID == "" {
		a.HNID = b.HNID
	}
	for _, source := range b.Sources {
		found := false
		for _, old := range a.Sources {
			if source == old {
				found = true
			}
		}
		if !found {
			a.Sources = append(a.Sources, source)
		}
	}
	sort.Strings(a.Sources)
	if len(b.Text) > len(a.Text) {
		a.Text = b.Text
		a.EvidenceURL = b.EvidenceURL
		a.ContentStatus = b.ContentStatus
	}
	if a.TimeBasis == "discussion_submission" && b.TimeBasis != "discussion_submission" {
		a.TimeBasis = b.TimeBasis
		a.PublishedAt = b.PublishedAt
		a.Title = b.Title
	}
	return a
}
func hotSourceLabels(sources []string) string {
	labels := map[string]string{"techmeme": "Techmeme", "rundown": "The Rundown AI", "openai": "OpenAI", "deepmind": "Google DeepMind", "qbitai": "量子位", "decoder": "THE DECODER", "hackernews": "Hacker News"}
	out := []string{}
	for _, v := range sources {
		label := labels[v]
		if label == "" {
			label = v
		}
		out = append(out, label)
	}
	return strings.Join(out, " / ")
}
