package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type NewsItem struct {
	Sources     []string  `json:"sources,omitempty"`
	TimeBasis   string    `json:"time_basis,omitempty"`
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	IssueURL    string    `json:"issue_url"`
	PublishedAt time.Time `json:"published_at"`
	Text        string    `json:"text"`
}
type NewsPage struct {
	SnapshotID string     `json:"snapshot_id,omitempty"`
	Status     string     `json:"status"`
	Since      time.Time  `json:"since"`
	Until      time.Time  `json:"until"`
	Ranking    string     `json:"ranking"`
	Items      []NewsItem `json:"items"`
	Note       string     `json:"note,omitempty"`
}
type NewsService struct {
	Home  string
	Fetch func(context.Context, string) (string, error)
}

func newsID(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s)))[:24] }
func (s NewsService) fetch(ctx context.Context, path string) (string, error) {
	if s.Fetch != nil {
		return s.Fetch(ctx, path)
	}
	client := http.Client{Timeout: 8 * time.Second, CheckRedirect: func(r *http.Request, _ []*http.Request) error {
		if r.URL.Scheme != "https" || r.URL.Host != "www.therundown.ai" {
			return fmt.Errorf("news redirect outside source")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.therundown.ai"+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Friday-personal-briefing/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("news source HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if len(raw) > 2*1024*1024 {
		return "", fmt.Errorf("news page too large")
	}
	return string(raw), err
}
func (s NewsService) Recent(ctx context.Context, until time.Time) NewsPage {
	page := NewsPage{Status: "ok", Since: until.Add(-24 * time.Hour), Until: until, Ranking: "editorial_selection_not_popularity", Items: []NewsItem{}}
	raw, err := s.fetch(ctx, "/articles-category/ai")
	if err != nil {
		page.Status = "unavailable"
		page.Note = "AI News来源暂不可用"
		return page
	}
	paths := newsIssuePaths(raw)
	if len(paths) == 0 {
		page.Status = "unavailable"
		page.Note = "新闻页面结构未识别"
		return page
	}
	seen := map[string]bool{}
	readable := 0
	for _, path := range paths {
		if ctx.Err() != nil {
			break
		}
		body, e := s.fetch(ctx, path)
		if e != nil {
			continue
		}
		readable++
		at, items := parseNewsIssue(body, "https://www.therundown.ai"+path)
		if at.IsZero() || at.Before(page.Since) || at.After(until) {
			continue
		}
		for _, item := range items {
			u, e := url.Parse(item.URL)
			if e != nil || u.Scheme != "https" || u.Host == "" {
				continue
			}
			key := strings.ToLower(item.Title)
			if seen[item.URL] || seen[key] {
				continue
			}
			seen[item.URL] = true
			seen[key] = true
			item.ID = newsID(item.URL)
			item.PublishedAt = at
			page.Items = append(page.Items, item)
			if len(page.Items) == 3 {
				break
			}
		}
		if len(page.Items) == 3 {
			break
		}
	}
	if readable == 0 {
		page.Status = "unavailable"
		page.Note = "AI News正文暂不可用"
	} else if len(page.Items) == 0 {
		page.Note = "近24小时暂无可确认的新条目"
	}
	return page
}
func (s NewsService) MarshalRecent(ctx context.Context, until time.Time) (string, error) {
	raw, e := json.Marshal(s.Recent(ctx, until))
	return string(raw), e
}
