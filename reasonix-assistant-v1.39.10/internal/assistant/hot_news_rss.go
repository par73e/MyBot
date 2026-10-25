package assistant

import (
	"encoding/xml"
	"errors"
	"golang.org/x/net/html"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type hotFeedItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Date        string `xml:"pubDate"`
	Description string `xml:"description"`
	Content     string `xml:"encoded"`
}
type hotFeed struct {
	Channel struct {
		Title string        `xml:"title"`
		Items []hotFeedItem `xml:"item"`
	} `xml:"channel"`
}

func newsPlain(raw string) string {
	n, e := html.Parse(strings.NewReader(raw))
	if e != nil {
		return ""
	}
	return nodeText(n)
}
func boundedNewsText(raw string, limit int) string {
	r := []rune(strings.TrimSpace(raw))
	if len(r) > limit {
		return string(r[:limit])
	}
	return string(r)
}
func hotNewsDate(raw string) (time.Time, error) {
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC3339, time.RFC3339Nano} {
		if t, e := time.Parse(layout, raw); e == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("news publication time not recognized")
}
func canonicalNewsURL(raw string) string {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || newsPublicURL(raw) != nil {
		return ""
	}
	u.Fragment = ""
	q := u.Query()
	for k := range q {
		if strings.HasPrefix(strings.ToLower(k), "utm_") || k == "ref" || k == "fbclid" {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u.String()
}
func techmemeOriginal(raw, fallback string) string {
	n, e := html.Parse(strings.NewReader(raw))
	if e != nil {
		return fallback
	}
	found := ""
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != "" {
			return
		}
		if n.Data == "a" {
			u := canonicalNewsURL(nodeAttr(n, "href"))
			p, _ := url.Parse(u)
			if u != "" && p.Hostname() != "www.techmeme.com" && p.Hostname() != "techmeme.com" {
				found = u
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	if found != "" {
		return found
	}
	return fallback
}

var hotAIWords = regexp.MustCompile(`(?i)(\b(ai|llm|gpt|chatgpt|openai|anthropic|claude|gemini|deepseek|qwen|copilot|cerebras|mistral|hugging face|machine learning|neural|artificial intelligence|agentic)\b|人工智能|大模型|智能体|生成式|推理模型|开源模型|具身智能|机器学习|多模态)`)
var hotNoise = regexp.MustCompile(`(?i)^(ask hn:|hiring|who is hiring|is ai just|what.s wrong with ai|that ai temptation)`)

func newsRelevant(title string) bool {
	return hotAIWords.MatchString(title) && !hotNoise.MatchString(title)
}
func parseHotRSS(raw []byte, source string, until time.Time) ([]HotNewsArticle, int, error) {
	var feed hotFeed
	if e := xml.Unmarshal(raw, &feed); e != nil {
		return nil, 0, e
	}
	if feed.Channel.Title == "" {
		return nil, 0, errors.New("RSS channel missing")
	}
	out := []HotNewsArticle{}
	since := until.Add(-24 * time.Hour)
	validDates := 0
	for _, item := range feed.Channel.Items {
		at, e := hotNewsDate(item.Date)
		if e == nil {
			validDates++
		}
		if e != nil || at.Before(since) || at.After(until) {
			continue
		}
		body := item.Content
		if body == "" {
			body = item.Description
		}
		title := newsPlain(item.Title)
		if source == "rundown" {
			_, items := parseNewsIssue(`<meta property="article:published_time" content="`+at.Format(time.RFC3339)+`">`+body, item.Link)
			if len(items) == 0 {
				return nil, len(feed.Channel.Items), errors.New("recent newsletter news structure not recognized")
			}
			for _, v := range items {
				u := canonicalNewsURL(v.URL)
				if u != "" {
					out = append(out, HotNewsArticle{ID: newsID(u), Title: v.Title, URL: u, EvidenceURL: item.Link, Sources: []string{source}, PublishedAt: at, TimeBasis: "newsletter_publication", Text: boundedNewsText(v.Text, 6000), MetricAt: until, ContentStatus: "source_excerpt"})
				}
			}
			continue
		}
		if !newsRelevant(title) {
			continue
		}
		u := canonicalNewsURL(item.Link)
		if source == "techmeme" {
			u = techmemeOriginal(body, u)
		}
		if u == "" {
			continue
		}
		out = append(out, HotNewsArticle{ID: newsID(u), Title: title, URL: u, EvidenceURL: item.Link, Sources: []string{source}, PublishedAt: at, TimeBasis: "source_publication", Text: boundedNewsText(newsPlain(body), 6000), MetricAt: until, ContentStatus: "source_excerpt"})
		if len(out) >= 40 {
			break
		}
	}
	if len(feed.Channel.Items) > 0 && validDates == 0 {
		return nil, len(feed.Channel.Items), errors.New("RSS publication times not recognized")
	}
	return out, len(feed.Channel.Items), nil
}
