package assistant

import (
	"golang.org/x/net/html"
	"strings"
	"time"
)

func nodeAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.ElementNode && (x.Data == "script" || x.Data == "style") {
			return
		}
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
			b.WriteByte(' ')
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}
func newsIssuePaths(raw string) []string {
	doc, e := html.Parse(strings.NewReader(raw))
	if e != nil {
		return nil
	}
	out := []string{}
	seen := map[string]bool{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			p := nodeAttr(n, "href")
			if strings.HasPrefix(p, "/articles/") && !strings.Contains(p, "?") && !seen[p] && len(out) < 8 {
				out = append(out, p)
				seen[p] = true
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}
func newsHeadingLink(n *html.Node) (string, string) {
	var title, link string
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Data == "a" && link == "" {
			link = nodeAttr(x, "href")
			title = nodeText(x)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return title, link
}
func parseNewsIssue(raw, issueURL string) (time.Time, []NewsItem) {
	doc, e := html.Parse(strings.NewReader(raw))
	if e != nil {
		return time.Time{}, nil
	}
	at := time.Time{}
	category := ""
	items := []NewsItem{}
	current := -1
	quick := false
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "meta":
				if nodeAttr(n, "property") == "article:published_time" {
					at, _ = time.Parse(time.RFC3339, nodeAttr(n, "content"))
				}
			case "h3":
				current = -1
				quick = strings.Contains(strings.ToLower(nodeText(n)), "everything else in ai")
			case "h6":
				category = strings.ToUpper(nodeText(n))
				current = -1
			case "h4":
				title, link := newsHeadingLink(n)
				excluded := false
				for _, word := range []string{"TOGETHER", "PRESENTED", "SPONSOR", "TRAINING", "WORKFLOW", "CORNER"} {
					if strings.Contains(category, word) {
						excluded = true
					}
				}
				if !excluded && title != "" && strings.HasPrefix(link, "https://") {
					items = append(items, NewsItem{Title: title, URL: link, IssueURL: issueURL})
					current = len(items) - 1
				} else {
					current = -1
				}
			case "p", "li":
				if quick {
					text := nodeText(n)
					_, link := newsHeadingLink(n)
					if len(text) > 30 && strings.HasPrefix(link, "https://") {
						items = append(items, NewsItem{Title: text, URL: link, IssueURL: issueURL, Text: text})
					}
					return
				}
				if current >= 0 && len(items[current].Text) < 5000 {
					items[current].Text += nodeText(n) + "\n"
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return at, items
}
