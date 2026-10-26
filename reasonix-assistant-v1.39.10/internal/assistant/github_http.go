package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/identitylock"
)

type githubHTTPState struct {
	ETag     string          `json:"etag,omitempty"`
	Modified string          `json:"modified,omitempty"`
	Next     string          `json:"next,omitempty"`
	Body     json.RawMessage `json:"body"`
}

type githubBackoff struct {
	Until time.Time `json:"until"`
}

func (s GitHubService) base() string {
	if s.apiBase != "" {
		return s.apiBase
	}
	return "https://api.github.com"
}

func (s GitHubService) credential() (string, error) {
	path := filepath.Join(s.Home, "credentials", "github-pr-monitor.token")
	if s.tokenFile != "" {
		path = s.tokenFile
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("GitHub credential unavailable or file permissions are not private")
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 4096 || strings.TrimSpace(string(data)) == "" {
		return "", errors.New("GitHub credential unavailable")
	}
	return strings.TrimSpace(string(data)), nil
}

func (s GitHubService) safeAPI(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	base, _ := url.Parse(s.base())
	if err != nil || u.Scheme != base.Scheme || u.Host != base.Host || u.User != nil || u.Fragment != "" {
		return nil, errors.New("GitHub API redirect/pagination left the configured API origin")
	}
	return u, nil
}

// Every HTTP request is GET-only and shares a cross-process serialization lease.
// Backoff is global; cached 304 pages retain their own pagination Link.
func (s GitHubService) get(ctx context.Context, path string, out any) (string, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return "", errors.New("invalid GitHub API path")
	}
	if err := os.MkdirAll(filepath.Dir(s.repo().Path), 0o700); err != nil {
		return "", err
	}
	unlock, err := identitylock.Acquire(ctx, s.repo().Path+".github-http")
	if err != nil {
		return "", err
	}
	defer unlock()
	cache, blocked, err := s.httpState(ctx, path)
	if err != nil {
		return "", err
	}
	if s.now().Before(blocked) {
		return "", errors.New("GitHub API is backing off; prior progress retained")
	}
	token, err := s.credential()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base()+path, nil)
	if err != nil {
		return "", errors.New("invalid GitHub API request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	req.Header.Set("User-Agent", "Friday-PR-Monitor")
	if cache.ETag != "" {
		req.Header.Set("If-None-Match", cache.ETag)
	} else if cache.Modified != "" {
		req.Header.Set("If-Modified-Since", cache.Modified)
	}
	client := http.Client{Transport: http.DefaultTransport}
	if s.Client != nil {
		client = *s.Client
	}
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many GitHub API redirects")
		}
		_, err := s.safeAPI(r.URL.String())
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("GitHub connection failed or timed out")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotModified {
		if len(cache.Body) == 0 || json.Unmarshal(cache.Body, out) != nil {
			return "", errors.New("GitHub returned 304 without a usable cached page")
		}
		return cache.Next, nil
	}
	if resp.StatusCode != http.StatusOK {
		var failure struct {
			Message string `json:"message"`
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = json.Unmarshal(body, &failure)
		limited := resp.Header.Get("Retry-After") != "" || resp.Header.Get("X-RateLimit-Remaining") == "0" || strings.Contains(strings.ToLower(failure.Message), "rate limit")
		if err := s.saveBackoff(ctx, resp, limited); err != nil {
			return "", err
		}
		return "", fmt.Errorf("GitHub HTTP %d; previous state retained, permissions/data may be unavailable", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, out) != nil {
		return "", errors.New("GitHub response is incomplete, oversized or invalid JSON")
	}
	next, err := s.nextPage(resp.Header.Get("Link"))
	if err != nil {
		return "", err
	}
	cache = githubHTTPState{ETag: resp.Header.Get("ETag"), Modified: resp.Header.Get("Last-Modified"), Next: next, Body: data}
	if err := s.saveHTTPState(ctx, path, cache); err != nil {
		return "", err
	}
	return next, nil
}

func (s GitHubService) nextPage(link string) (string, error) {
	for item := range strings.SplitSeq(link, ",") {
		parts := strings.Split(item, ";")
		if len(parts) < 2 || !strings.Contains(strings.Join(parts[1:], ";"), `rel="next"`) {
			continue
		}
		u, err := s.safeAPI(strings.Trim(strings.TrimSpace(parts[0]), "<>"))
		if err != nil {
			return "", err
		}
		return u.RequestURI(), nil
	}
	return "", nil
}

func (s GitHubService) saveBackoff(ctx context.Context, resp *http.Response, limited bool) error {
	if resp.StatusCode == http.StatusForbidden && !limited {
		return nil
	}
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < http.StatusInternalServerError {
		return nil
	}
	until := s.now().Add(time.Minute)
	if n, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && n > 0 {
		until = s.now().Add(time.Duration(n) * time.Second)
	} else if at, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
		until = at
	}
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		if n, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil && time.Unix(n, 0).After(until) {
			until = time.Unix(n, 0)
		}
	}
	return s.persistBackoff(ctx, githubBackoff{Until: until})
}
