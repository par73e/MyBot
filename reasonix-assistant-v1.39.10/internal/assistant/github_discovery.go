package assistant

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

func itoa(v int) string { return strconv.Itoa(v) }

// Complete every search page before persisting discoveries or a successful
// discovery timestamp. Search's 1000-result ceiling is never treated as complete.
func (s GitHubService) discover(ctx context.Context, manual bool) (int, error) {
	cfg, err := s.Config(ctx)
	if err != nil {
		return 0, err
	}
	if cfg.Login == "" || !cfg.AutoDiscover || !cfg.Enabled {
		return 0, nil
	}
	if !manual && s.now().Before(cfg.DiscoveryAt) {
		return 0, nil
	}
	urls, err := s.discoveryURLs(ctx, cfg.Login)
	if err != nil {
		return 0, err
	}
	added := 0
	err = s.repo().transact(ctx, func(tx *sql.Tx) error {
		var current GitHubConfig
		if e := githubLoad(ctx, tx, "github_config", "default", &current); e != nil {
			return e
		}
		if !current.Enabled || !current.AutoDiscover || current.Revision != cfg.Revision {
			return errors.New("GitHub discovery configuration changed")
		}
		all, e := loadEntities[GitHubWatch](ctx, tx, "github_pr_watch")
		if e != nil {
			return e
		}
		for _, raw := range urls {
			repo, n, _ := parseGitHubPR(raw)
			id := "gh-watch-" + mailHash(repo+":"+itoa(n))
			var w GitHubWatch
			e := githubLoad(ctx, tx, "github_pr_watch", id, &w)
			if e == nil {
				if w.Excluded || !w.Finished {
					continue
				}
				w.Finished = false
				w.Enabled = true
				w.NextCheckAt = s.now()
				w.Revision++
			} else if errors.Is(e, sql.ErrNoRows) {
				if len(all)+added >= 500 {
					return errors.New("GitHub watch capacity reached")
				}
				w = GitHubWatch{ID: id, Revision: 1, URL: raw, Repository: repo, Number: n, Enabled: true, Automatic: true}
				added++
			} else {
				return e
			}
			if e := saveEntity(ctx, tx, "github_pr_watch", id, w); e != nil {
				return e
			}
		}
		current.DiscoveryOKAt = s.now()
		current.DiscoveryAt = githubNextCheck(s.now(), githubDiscoverySeconds(current))
		current.DiscoveryErr = ""
		return saveEntity(ctx, tx, "github_config", "default", current)
	})
	return added, err
}

func (s GitHubService) discoveryURLs(ctx context.Context, login string) ([]string, error) {
	query := "is:pr is:open is:public author:" + login
	path := "/search/issues?per_page=100&sort=created&order=asc&q=" + url.QueryEscape(query)
	urls := []string{}
	seen := map[string]bool{}
	for page := 0; path != ""; page++ {
		if page >= 10 || seen[path] {
			return nil, errors.New("GitHub PR discovery pagination is incomplete")
		}
		seen[path] = true
		var data struct {
			Total      int  `json:"total_count"`
			Incomplete bool `json:"incomplete_results"`
			Items      []struct {
				URL  string     `json:"html_url"`
				User githubUser `json:"user"`
				PR   *struct {
					URL string `json:"url"`
				} `json:"pull_request"`
			} `json:"items"`
		}
		next, e := s.get(ctx, path, &data)
		if e != nil {
			return nil, e
		}
		if data.Incomplete || data.Total > 200 {
			return nil, errors.New("GitHub discovery incomplete or exceeds 200 public open PRs; existing watches retained")
		}
		for _, v := range data.Items {
			if v.PR == nil || !strings.EqualFold(v.User.Login, login) {
				return nil, errors.New("GitHub discovery returned an unexpected author/resource")
			}
			if _, _, e := parseGitHubPR(v.URL); e != nil {
				return nil, e
			}
			urls = append(urls, v.URL)
		}
		path = next
		if path == "" && len(urls) != data.Total {
			return nil, errors.New("GitHub discovery returned fewer PRs than its total")
		}
	}
	return urls, nil
}
