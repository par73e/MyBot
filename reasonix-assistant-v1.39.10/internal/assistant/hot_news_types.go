package assistant

import (
	"context"
	"time"
)

type HotNewsService struct {
	Home  string
	Fetch func(context.Context, string) ([]byte, error)
	Clock func() time.Time
}
type NewsSourceState struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Status string `json:"status"`
	Items  int    `json:"items"`
	Recent int    `json:"recent"`
	Error  string `json:"error,omitempty"`
}
type HotNewsArticle struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	URL           string    `json:"url"`
	EvidenceURL   string    `json:"evidence_url"`
	Sources       []string  `json:"sources"`
	PublishedAt   time.Time `json:"published_at"`
	TimeBasis     string    `json:"time_basis"`
	Text          string    `json:"text,omitempty"`
	Points        int       `json:"hn_points,omitempty"`
	Comments      int       `json:"hn_comments,omitempty"`
	HNID          string    `json:"hn_id,omitempty"`
	ReadAt        time.Time `json:"read_at,omitempty"`
	MetricAt      time.Time `json:"metrics_at"`
	RankScore     float64   `json:"rank_score"`
	ContentStatus string    `json:"content_status"`
}
type HotNewsSnapshot struct {
	ID           string            `json:"id"`
	Since        time.Time         `json:"since"`
	AsOf         time.Time         `json:"as_of"`
	Status       string            `json:"status"`
	Ranking      string            `json:"ranking"`
	Sources      []NewsSourceState `json:"sources"`
	Candidates   []HotNewsArticle  `json:"candidates"`
	TargetCount  int               `json:"target_count"`
	Instructions string            `json:"instructions"`
}
type NewsEventSummary struct {
	IDs     []string `json:"ids"`
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
}
type NewsBriefingOutput struct {
	Events []NewsEventSummary `json:"events"`
	Note   string             `json:"note,omitempty"`
}

func (s HotNewsService) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}
