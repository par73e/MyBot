package assistant

import (
	"context"
	"strings"
	"time"
)

// Reuse ranked facts and frozen reads; the morning snapshot saves only its three
// displayed references, not the full collector payload or standalone prose.
func (s MorningService) prepareHotNews(ctx context.Context) NewsPage {
	ns := HotNewsService{Home: s.Home, Clock: s.Clock}
	if s.News != nil {
		ns = *s.News
	}
	page := NewsPage{Status: "unavailable", Since: s.now().Add(-24 * time.Hour), Until: s.now(), Items: []NewsItem{}}
	snap, err := ns.snapshot(ctx, "")
	if err != nil {
		return page
	}
	page.SnapshotID, page.Since, page.Until, page.Ranking, page.Status = snap.ID, snap.Since, snap.AsOf, snap.Ranking, snap.Status
	seen := map[string]bool{}
	for at := 0; at < len(snap.Candidates) && len(page.Items) < 3 && ctx.Err() == nil; {
		ids := []string{}
		for at < len(snap.Candidates) && len(ids) < 3-len(page.Items) {
			a := snap.Candidates[at]
			at++
			key := strings.ToLower(singleLine(a.Title))
			if seen[key] {
				continue
			}
			seen[key] = true
			ids = append(ids, a.ID)
		}
		if len(ids) == 0 {
			continue
		}
		articles, e := ns.read(ctx, &snap, ids)
		if e != nil {
			page.Status = "partial"
			break
		}
		for _, a := range articles {
			if a.ContentStatus == "insufficient_content" || a.ReadAt.IsZero() {
				continue
			}
			page.Items = append(page.Items, NewsItem{ID: a.ID, Title: boundedNewsText(a.Title, 160), URL: a.URL, IssueURL: a.EvidenceURL, PublishedAt: a.PublishedAt, Text: boundedNewsBytes(a.Text, 1800), Sources: a.Sources, TimeBasis: a.TimeBasis})
		}
	}
	if len(page.Items) == 0 && len(snap.Candidates) > 0 {
		page.Status = "unavailable"
	}
	return page
}
