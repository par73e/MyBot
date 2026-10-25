package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"golang.org/x/net/html"
	"strings"
	"sync"
	"time"
)

func articleNewsContent(raw []byte) (string, time.Time) {
	doc, e := html.Parse(strings.NewReader(string(raw)))
	if e != nil {
		return "", time.Time{}
	}
	var article, main *html.Node
	description := ""
	published := time.Time{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Data == "article" && article == nil {
			article = n
		}
		if n.Data == "main" && main == nil {
			main = n
		}
		if n.Data == "meta" {
			key := nodeAttr(n, "property")
			if key == "" {
				key = nodeAttr(n, "name")
			}
			content := nodeAttr(n, "content")
			if key == "article:published_time" || key == "datePublished" {
				published, _ = time.Parse(time.RFC3339, content)
			}
			if key == "description" || key == "og:description" {
				description = content
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if article != nil {
		return boundedNewsText(nodeText(article), 7000), published
	}
	if main != nil {
		return boundedNewsText(nodeText(main), 7000), published
	}
	return boundedNewsText(description, 2000), published
}
func (s HotNewsService) readArticle(ctx context.Context, a HotNewsArticle) HotNewsArticle {
	if !a.ReadAt.IsZero() {
		return a
	}
	if a.HNID != "" {
		raw, e := s.fetchPublic(ctx, "https://hacker-news.firebaseio.com/v0/item/"+a.HNID+".json")
		var item struct {
			ID       int    `json:"id"`
			Type     string `json:"type"`
			Deleted  bool   `json:"deleted"`
			Dead     bool   `json:"dead"`
			Score    int    `json:"score"`
			Comments int    `json:"descendants"`
			URL      string `json:"url"`
		}
		if e == nil && json.Unmarshal(raw, &item) == nil && item.ID > 0 && item.Type == "story" && !item.Deleted && !item.Dead && canonicalNewsURL(item.URL) == a.URL {
			a.Points = item.Score
			a.Comments = item.Comments
			a.MetricAt = s.now()
		}
	}
	if len([]rune(a.Text)) < 1600 {
		raw, e := s.fetchPublic(ctx, a.URL)
		if e == nil {
			text, published := articleNewsContent(raw)
			if len(text) > len(a.Text) && !strings.Contains(strings.ToLower(text), "enable javascript and cookies") {
				a.Text = text
				a.ContentStatus = "original_excerpt"
				a.EvidenceURL = a.URL
			}
			if !published.IsZero() {
				a.PublishedAt = published
				a.TimeBasis = "original_publication"
			}
		}
	}
	if len([]rune(a.Text)) < 70 {
		a.ContentStatus = "insufficient_content"
	}
	if a.ContentStatus != "insufficient_content" {
		a.ReadAt = s.now()
	}
	return a
}
func (s HotNewsService) read(ctx context.Context, snap *HotNewsSnapshot, ids []string) ([]HotNewsArticle, error) {
	if len(ids) == 0 || len(ids) > 7 {
		return nil, errors.New("read requires 1..7 candidate IDs")
	}
	indices := []int{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, errors.New("duplicate news read ID")
		}
		seen[id] = true
		found := -1
		for i := range snap.Candidates {
			if snap.Candidates[i].ID == id {
				found = i
				break
			}
		}
		if found < 0 {
			return nil, errors.New("news ID is not in this snapshot")
		}
		indices = append(indices, found)
	}
	out := make([]HotNewsArticle, len(indices))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3)
	for i, at := range indices {
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				out[i] = snap.Candidates[at]
				out[i].ContentStatus = "insufficient_content"
				return
			}
			defer func() { <-sem }()
			out[i] = s.readArticle(ctx, snap.Candidates[at])
		})
	}
	wg.Wait()
	return s.commitArticleReads(ctx, snap, out)
}
func (s HotNewsService) commitArticleReads(ctx context.Context, snap *HotNewsSnapshot, read []HotNewsArticle) ([]HotNewsArticle, error) {
	result := []HotNewsArticle{}
	err := NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		var raw string
		if e := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='ai_news_snapshot' AND id=?", snap.ID).Scan(&raw); e != nil {
			return e
		}
		var current HotNewsSnapshot
		if e := json.Unmarshal([]byte(raw), &current); e != nil {
			return e
		}
		for _, a := range read {
			for i, old := range current.Candidates {
				if old.ID != a.ID {
					continue
				}
				if !old.ReadAt.IsZero() {
					a = old
				}
				current.Candidates[i] = a
				result = append(result, a)
				break
			}
		}
		*snap = current
		return saveEntity(ctx, tx, "ai_news_snapshot", current.ID, current)
	})
	return result, err
}
