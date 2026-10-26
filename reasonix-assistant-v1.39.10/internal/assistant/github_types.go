package assistant

import (
	"net/http"
	"time"
)

type GitHubService struct {
	Home      string
	Clock     func() time.Time
	Client    *http.Client
	apiBase   string // Test injection only; never accepted by tools or configuration.
	tokenFile string // Tests use isolated credentials; production has a fixed path.
}

func (s GitHubService) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

func (s GitHubService) repo() Repository { return NewRepository(s.Home) }

type GitHubConfig struct {
	Revision         int       `json:"revision"`
	Enabled          bool      `json:"enabled"`
	Login            string    `json:"login"`
	AutoDiscover     bool      `json:"auto_discover"`
	PollSeconds      int       `json:"poll_seconds"`
	DiscoverySeconds int       `json:"discovery_seconds"`
	DiscoveryAt      time.Time `json:"discovery_at,omitempty"`
	DiscoveryOKAt    time.Time `json:"discovery_ok_at,omitempty"`
	DiscoveryErr     string    `json:"discovery_error,omitempty"`
}

type GitHubWatch struct {
	ID           string          `json:"id"`
	Revision     int             `json:"revision"`
	URL          string          `json:"url"`
	Repository   string          `json:"repository"`
	Number       int             `json:"number"`
	Enabled      bool            `json:"enabled"`
	Finished     bool            `json:"finished"`
	Automatic    bool            `json:"automatic"`
	Excluded     bool            `json:"excluded"`
	Baseline     *GitHubSnapshot `json:"baseline,omitempty"`
	NextCheckAt  time.Time       `json:"next_check_at"`
	CheckedAt    time.Time       `json:"checked_at,omitempty"`
	Error        string          `json:"error,omitempty"`
	Failures     int             `json:"failures,omitempty"`
	FailedCIHead string          `json:"failed_ci_head,omitempty"`
	FailedNames  []string        `json:"failed_ci_names,omitempty"`
}

type GitHubSnapshot struct {
	UntrustedExternalData bool                  `json:"untrusted_external_data"`
	URL                   string                `json:"url"`
	Repository            string                `json:"repository"`
	Number                int                   `json:"number"`
	Title                 string                `json:"title"`
	Author                string                `json:"author"`
	State                 string                `json:"state"`
	HeadSHA               string                `json:"head_sha"`
	FetchedAt             time.Time             `json:"fetched_at"`
	Items                 map[string]GitHubItem `json:"items"`
	CI                    GitHubCI              `json:"ci"`
}

type GitHubItem struct {
	Body          string `json:"-"` // Current fetch only; persisted baselines retain previews.
	BodyTruncated bool   `json:"-"`
	MentionsOwner bool   `json:"mentions_owner,omitempty"`
	TextTruncated bool   `json:"text_truncated,omitempty"`
	Sequence      int64  `json:"sequence"`
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Author        string `json:"author"`
	Bot           bool   `json:"bot"`
	State         string `json:"state,omitempty"`
	CommitSHA     string `json:"commit_sha,omitempty"`
	URL           string `json:"url"`
	Text          string `json:"text"`
	Fingerprint   string `json:"fingerprint"`
	UpdatedAt     string `json:"updated_at"`
}

type GitHubCI struct {
	State   string   `json:"state"` // unknown, pending, failure, success
	Failed  []string `json:"failed"`
	Error   string   `json:"error,omitempty"`
	HeadSHA string   `json:"head_sha"`
}

type GitHubEvent struct {
	Author        string    `json:"author,omitempty"`
	Label         string    `json:"label,omitempty"`
	Body          string    `json:"body,omitempty"`
	BodyTruncated bool      `json:"body_truncated,omitempty"`
	SourceID      string    `json:"source_id,omitempty"`
	Fingerprint   string    `json:"fingerprint,omitempty"`
	Superseded    bool      `json:"superseded,omitempty"`
	ID            string    `json:"id"`
	WatchID       string    `json:"watch_id"`
	Kind          string    `json:"kind"`
	HeadSHA       string    `json:"head_sha,omitempty"`
	State         string    `json:"state,omitempty"`
	Text          string    `json:"text"`
	URL           string    `json:"url"`
	ObservedAt    time.Time `json:"observed_at"`
	DeliveredAt   time.Time `json:"delivered_at,omitempty"`
	JobID         string    `json:"job_id,omitempty"`
}

type GitHubRequest struct {
	PollSeconds      *int   `json:"poll_seconds,omitempty"`
	DiscoverySeconds *int   `json:"discovery_seconds,omitempty"`
	Action           string `json:"action"`
	ID               string `json:"id,omitempty"`
	Revision         int    `json:"revision,omitempty"`
	URL              string `json:"url,omitempty"`
	Login            string `json:"login,omitempty"`
	Enabled          *bool  `json:"enabled,omitempty"`
	AutoDiscover     *bool  `json:"auto_discover,omitempty"`
}
