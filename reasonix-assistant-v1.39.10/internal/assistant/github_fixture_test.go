package assistant

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type githubFixture struct {
	mu                        sync.Mutex
	sha, state, check         string
	comments, inline, reviews []map[string]any
	failPath                  string
	failCode                  int
	pages                     bool
	discovery                 bool
	incomplete                bool
	requests, notModified     int
	server                    *httptest.Server
	now                       time.Time
}

func githubFixtureService(t *testing.T) (HeartbeatService, GitHubService, *githubFixture) {
	t.Helper()
	hs, _ := unifiedFixture(t)
	if err := hs.repo().transact(t.Context(), func(tx *sql.Tx) error {
		cfg, err := loadHeartbeatConfig(t.Context(), tx)
		if err != nil {
			return err
		}
		for day, window := range cfg.Weekly {
			window.End = "23:30"
			cfg.Weekly[day] = window
		}
		return saveEntity(t.Context(), tx, "heartbeat_config", heartbeatConfigID, cfg)
	}); err != nil {
		t.Fatal(err)
	}
	f := &githubFixture{sha: strings.Repeat("a", 40), state: "open", check: "success", comments: []map[string]any{}, inline: []map[string]any{}, reviews: []map[string]any{}, now: timeMust("2026-10-27T10:00:00+08:00")}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	credential := filepath.Join(hs.Home, "fixture-token")
	if err := os.WriteFile(credential, []byte("fixture-token-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := GitHubService{Home: hs.Home, Clock: func() time.Time { return f.now }, Client: f.server.Client(), apiBase: f.server.URL, tokenFile: credential}
	return hs, s, f
}

func (f *githubFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer fixture-token-value" || r.Header.Get("X-GitHub-Api-Version") != "2026-03-10" {
		http.Error(w, "unexpected method or authentication", http.StatusBadRequest)
		return
	}
	if f.failPath != "" && strings.Contains(r.URL.Path, f.failPath) {
		if f.failCode == 429 {
			w.Header().Set("Retry-After", "60")
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", fmt.Sprint(f.now.Add(2*time.Minute).Unix()))
		}
		http.Error(w, "untrusted error body with fixture-token-value", f.failCode)
		return
	}
	var result any
	switch {
	case r.URL.Path == "/user":
		result = map[string]any{"login": "owner"}
	case r.URL.Path == "/search/issues":
		items := []map[string]any{}
		if f.discovery {
			items = append(items, map[string]any{"html_url": "https://github.com/org/project/pull/1", "user": map[string]string{"login": "owner"}, "pull_request": map[string]string{"url": "api"}})
		}
		result = map[string]any{"total_count": len(items), "incomplete_results": f.incomplete, "items": items}
	case r.URL.Path == "/repos/org/project/pulls/1":
		result = map[string]any{"number": 1, "html_url": "https://github.com/org/project/pull/1", "title": "Fix boundary handling", "state": f.state, "merged": f.state == "merged", "user": map[string]string{"login": "owner"}, "head": map[string]any{"sha": f.sha, "repo": map[string]any{"full_name": "org/project", "private": false}}, "base": map[string]any{"repo": map[string]any{"full_name": "org/project", "private": false}}}
		if f.state == "merged" {
			result.(map[string]any)["state"] = "closed"
		}
	case strings.Contains(r.URL.Path, "/issues/1/comments"):
		result = f.comments
		if f.pages && len(f.comments) > 1 {
			if r.URL.Query().Get("page") == "2" {
				result = f.comments[1:]
			} else {
				result = f.comments[:1]
				w.Header().Set("Link", "<"+f.server.URL+"/repos/org/project/issues/1/comments?per_page=100&page=2>; rel=\"next\"")
			}
		}
	case strings.HasSuffix(r.URL.Path, "/pulls/1/reviews"):
		result = f.reviews
	case strings.HasSuffix(r.URL.Path, "/pulls/1/comments"):
		result = f.inline
	case strings.HasSuffix(r.URL.Path, "/check-runs"):
		status := "completed"
		if f.check == "pending" {
			status = "in_progress"
		}
		result = map[string]any{"total_count": 1, "check_runs": []map[string]any{{"id": 1, "name": "unit-tests", "head_sha": f.sha, "status": status, "conclusion": f.check, "app": map[string]int{"id": 1}}}}
	case strings.HasSuffix(r.URL.Path, "/statuses"):
		result = []any{}
	default:
		http.Error(w, "unexpected API route", http.StatusNotFound)
		return
	}
	data, _ := json.Marshal(result)
	etag := fmt.Sprintf(`"%x"`, sha256.Sum256(data))
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		f.notModified++
		w.Header().Del("Link")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func githubCommentFixture(id int, author, body string) map[string]any {
	return map[string]any{"id": id, "user": map[string]string{"login": author, "type": "User"}, "body": body, "html_url": "https://github.com/org/project/pull/1#issuecomment-" + itoa(id), "updated_at": "2026-10-27T02:00:00Z"}
}

func configureGitHubFixture(t *testing.T, s GitHubService, discovery bool) GitHubConfig {
	t.Helper()
	cfg, err := s.configure(t.Context(), GitHubRequest{Login: "owner", AutoDiscover: &discovery})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func githubEventsForTest(t *testing.T, s GitHubService) []GitHubEvent {
	t.Helper()
	var out []GitHubEvent
	if err := s.repo().transact(t.Context(), func(tx *sql.Tx) error {
		var e error
		out, e = loadEntities[GitHubEvent](t.Context(), tx, "github_pr_event")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	return out
}
