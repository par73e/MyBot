package assistant

import (
	"net/http"
	"time"
)

// AlertService is separate from paid weather queries. Origins are fixed in
// production; the unexported transport hook only serves deterministic fixtures.
type AlertService struct {
	Home       string
	Clock      func() time.Time
	Client     *http.Client
	testOrigin string
}

func (s AlertService) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}
func (s AlertService) repo() Repository { return NewRepository(s.Home) }

type AlertConfig struct {
	Revision    int    `json:"revision"`
	Enabled     bool   `json:"enabled"`
	Region      string `json:"region"`
	PollSeconds int    `json:"poll_seconds"`
}
type OfficialAlert struct {
	Key         string    `json:"key"`
	Identifier  string    `json:"identifier"`
	References  string    `json:"references,omitempty"`
	Area        string    `json:"area"`
	Hazard      string    `json:"hazard"`
	Level       string    `json:"level"`
	Rank        int       `json:"rank"`
	PublishedAt time.Time `json:"published_at"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
	Cancel      bool      `json:"cancel"`
	Body        string    `json:"body,omitempty"`
	Source      string    `json:"source"`
	URL         string    `json:"url"`
}
type AlertRecord struct {
	Alert           OfficialAlert `json:"alert"`
	Revision        int           `json:"revision"`
	ObservedAt      time.Time     `json:"observed_at"`
	LastDeliveredAt time.Time     `json:"last_delivered_at,omitempty"`
	EventID         string        `json:"event_id,omitempty"`
}
type AlertEvent struct {
	ID          string        `json:"id"`
	Key         string        `json:"key"`
	Kind        string        `json:"kind"`
	Alert       OfficialAlert `json:"alert"`
	ObservedAt  time.Time     `json:"observed_at"`
	DeliveredAt time.Time     `json:"delivered_at,omitempty"`
	JobID       string        `json:"job_id,omitempty"`
	Superseded  bool          `json:"superseded,omitempty"`
}
type AlertHealth struct {
	Source       string    `json:"source"`
	CheckedAt    time.Time `json:"checked_at"`
	LastSuccess  time.Time `json:"last_success,omitempty"`
	NextAttempt  time.Time `json:"next_attempt"`
	Failures     int       `json:"failures"`
	Error        string    `json:"error,omitempty"`
	Count        int       `json:"count"`
	DetailErrors int       `json:"detail_errors"`
}
type alertCache struct {
	Body         []byte    `json:"body"`
	ETag         string    `json:"etag,omitempty"`
	Modified     string    `json:"modified,omitempty"`
	CheckedAt    time.Time `json:"checked_at"`
	BlockedUntil time.Time `json:"blocked_until,omitempty"`
}
type AlertRequest struct {
	Action      string `json:"action"`
	Revision    int    `json:"revision,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"`
	PollSeconds *int   `json:"poll_seconds,omitempty"`
}
