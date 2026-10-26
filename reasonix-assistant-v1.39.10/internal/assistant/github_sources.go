package assistant

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var githubName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
var githubMention = regexp.MustCompile(`@([A-Za-z0-9][A-Za-z0-9-]{0,38})\b`)

var githubSHA = regexp.MustCompile(`^[0-9a-fA-F]{40,64}$`)

func parseGitHubPR(raw string) (string, int, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || u.User != nil || u.RawQuery != "" || u.RawPath != "" {
		return "", 0, errors.New("provide a public https://github.com/OWNER/REPO/pull/NUMBER URL")
	}
	parts := strings.Split(strings.TrimSuffix(u.Path, "/"), "/")
	if len(parts) != 5 || parts[0] != "" || parts[3] != "pull" || !githubName.MatchString(parts[1]) || !githubName.MatchString(parts[2]) || parts[1] == "." || parts[1] == ".." || parts[2] == "." || parts[2] == ".." {
		return "", 0, errors.New("invalid GitHub PR URL")
	}
	n, err := strconv.Atoi(parts[4])
	if err != nil || n <= 0 {
		return "", 0, errors.New("invalid GitHub PR number")
	}
	return strings.ToLower(parts[1] + "/" + parts[2]), n, nil
}

type githubPR struct {
	Number int        `json:"number"`
	URL    string     `json:"html_url"`
	Title  string     `json:"title"`
	State  string     `json:"state"`
	Merged bool       `json:"merged"`
	User   githubUser `json:"user"`
	Head   struct {
		SHA  string     `json:"sha"`
		Repo githubRepo `json:"repo"`
	} `json:"head"`
	Base struct {
		Repo githubRepo `json:"repo"`
	} `json:"base"`
}

type githubUser struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}
type githubRepo struct {
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
}

type githubComment struct {
	ID          int64      `json:"id"`
	User        githubUser `json:"user"`
	Body        string     `json:"body"`
	URL         string     `json:"html_url"`
	State       string     `json:"state"`
	CommitID    string     `json:"commit_id"`
	UpdatedAt   string     `json:"updated_at"`
	SubmittedAt string     `json:"submitted_at"`
}

func githubPages[T any](ctx context.Context, s GitHubService, path string) ([]T, error) {
	out := []T{}
	seen := map[string]bool{}
	for page := 0; path != ""; page++ {
		if page >= 10 || seen[path] {
			return nil, errors.New("GitHub pagination limit/cycle reached; full baseline not advanced")
		}
		seen[path] = true
		var items []T
		next, err := s.get(ctx, path, &items)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		path = next
	}
	return out, nil
}

func (s GitHubService) Snapshot(ctx context.Context, rawURL string) (GitHubSnapshot, error) {
	var snap GitHubSnapshot
	repo, number, err := parseGitHubPR(rawURL)
	if err != nil {
		return snap, err
	}
	path := fmt.Sprintf("/repos/%s/pulls/%d", repo, number)
	var pr githubPR
	if _, err = s.get(ctx, path, &pr); err != nil {
		return snap, err
	}
	if pr.Number != number || pr.Base.Repo.Private || pr.Base.Repo.FullName == "" || !githubSHA.MatchString(pr.Head.SHA) || (pr.State != "open" && pr.State != "closed") {
		return snap, errors.New("PR is private or metadata is incomplete; public-only tracking retained")
	}
	canonicalRepo, canonicalNumber, err := parseGitHubPR(pr.URL)
	if err != nil || canonicalNumber != number || !strings.EqualFold(canonicalRepo, pr.Base.Repo.FullName) {
		return snap, errors.New("GitHub returned inconsistent PR identity")
	}
	snap = GitHubSnapshot{UntrustedExternalData: true, URL: pr.URL, Repository: canonicalRepo, Number: number, Title: boundedNewsText(pr.Title, 140), Author: pr.User.Login, State: pr.State, HeadSHA: pr.Head.SHA, FetchedAt: s.now(), Items: map[string]GitHubItem{}}
	if pr.Merged {
		snap.State = "merged"
	}
	cfg, err := s.Config(ctx)
	if err != nil {
		return GitHubSnapshot{}, err
	}
	for _, list := range []struct{ kind, path string }{
		{"comment", fmt.Sprintf("/repos/%s/issues/%d/comments?per_page=100", canonicalRepo, number)},
		{"review", fmt.Sprintf("/repos/%s/pulls/%d/reviews?per_page=100", canonicalRepo, number)},
		{"inline", fmt.Sprintf("/repos/%s/pulls/%d/comments?per_page=100", canonicalRepo, number)},
	} {
		items, err := githubPages[githubComment](ctx, s, list.path)
		if err != nil {
			return GitHubSnapshot{}, err
		}
		for _, v := range items {
			if len(snap.Items) >= 1500 {
				return GitHubSnapshot{}, errors.New("PR history exceeds supported snapshot size; baseline not advanced")
			}
			if v.ID <= 0 || v.User.Login == "" {
				return GitHubSnapshot{}, errors.New("incomplete GitHub discussion item")
			}
			stamp := v.UpdatedAt
			if stamp == "" || list.kind == "review" && v.SubmittedAt != "" {
				stamp = v.SubmittedAt
			}
			key := list.kind + ":" + strconv.FormatInt(v.ID, 10)
			// Keep a bounded body for new-event summaries without enlarging cached baselines.
			body := boundedNewsBytes(v.Body, 16000)
			snap.Items[key] = GitHubItem{MentionsOwner: githubMentionsOwner(v.Body, cfg.Login), TextTruncated: utf8.RuneCountInString(v.Body) > 240, ID: key, Sequence: v.ID, Kind: list.kind, Author: v.User.Login, Bot: v.User.Type == "Bot" || strings.HasSuffix(v.User.Login, "[bot]"), State: v.State, CommitSHA: v.CommitID, URL: githubPublicLink(v.URL, pr.URL), Text: boundedNewsText(v.Body, 240), Fingerprint: mailHash(v.Body + "\x00" + v.State), UpdatedAt: stamp}
			item := snap.Items[key]
			item.Body = body
			item.BodyTruncated = len(body) < len(v.Body)
			snap.Items[key] = item
		}
	}
	snap.CI = s.readCI(ctx, pr)
	// A force-push while paging must not bind old-head CI to a new PR snapshot.
	var latest githubPR
	if _, err = s.get(ctx, path, &latest); err != nil {
		return GitHubSnapshot{}, err
	}
	if latest.Head.SHA != pr.Head.SHA || latest.State != pr.State || latest.Merged != pr.Merged {
		return GitHubSnapshot{}, errors.New("PR head/state changed while reading; retry without advancing baseline")
	}
	return snap, nil
}

func githubPublicLink(raw, fallback string) string {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil {
		return fallback
	}
	return u.String()
}

func githubMentionsOwner(body, login string) bool {
	if login == "" {
		return false
	}
	for _, match := range githubMention.FindAllStringSubmatch(body, -1) {
		if strings.EqualFold(match[1], login) {
			return true
		}
	}
	return false
}
