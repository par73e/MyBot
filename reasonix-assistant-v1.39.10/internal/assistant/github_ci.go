package assistant

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type githubCheck struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	SHA        string `json:"head_sha"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	App        struct {
		ID int64 `json:"id"`
	} `json:"app"`
}

type githubStatus struct {
	ID        int64  `json:"id"`
	Context   string `json:"context"`
	State     string `json:"state"`
	CreatedAt string `json:"created_at"`
}

func (s GitHubService) readCI(ctx context.Context, pr githubPR) GitHubCI {
	out := GitHubCI{State: "unknown", HeadSHA: pr.Head.SHA, Failed: []string{}}
	checks, statuses := s.collectCI(ctx, pr, &out)
	pending, success := false, false
	failed := map[string]bool{}
	for _, v := range checks {
		if v.Status != "completed" {
			pending = true
			continue
		}
		switch v.Conclusion {
		case "success":
			success = true
		case "failure", "timed_out", "cancelled", "action_required", "startup_failure":
			failed[boundedNewsText(v.Name, 80)] = true
		case "neutral", "skipped":
		default:
			out.Error = "Some check conclusions are unknown"
		}
	}
	for _, v := range statuses {
		switch v.State {
		case "success":
			success = true
		case "failure", "error":
			failed[boundedNewsText(v.Context, 80)] = true
		case "pending":
			pending = true
		default:
			out.Error = "Some commit statuses are unknown"
		}
	}
	for name := range failed {
		out.Failed = append(out.Failed, name)
	}
	sort.Strings(out.Failed)
	switch {
	case len(out.Failed) > 0:
		out.State = "failure"
	case out.Error != "":
		out.State = "unknown"
	case pending:
		out.State = "pending"
	case success:
		out.State = "success"
	}
	return out
}

func (s GitHubService) collectCI(ctx context.Context, pr githubPR, out *GitHubCI) (map[string]githubCheck, map[string]githubStatus) {
	repos := []string{strings.ToLower(pr.Base.Repo.FullName)}
	if pr.Head.Repo.FullName != "" && !pr.Head.Repo.Private && !strings.EqualFold(pr.Head.Repo.FullName, pr.Base.Repo.FullName) {
		repos = append(repos, strings.ToLower(pr.Head.Repo.FullName))
	}
	checks := map[string]githubCheck{}
	statuses := map[string]githubStatus{}
	for _, repo := range repos {
		if !validGitHubRepository(repo) {
			out.Error = "CI repository identity is invalid"
			continue
		}
		path := fmt.Sprintf("/repos/%s/commits/%s/check-runs?per_page=100&filter=latest", repo, pr.Head.SHA)
		seen := map[string]bool{}
		readRuns, totalRuns := 0, 0
		for page := 0; path != ""; page++ {
			if page >= 10 || seen[path] {
				out.Error = "CI pagination incomplete"
				break
			}
			seen[path] = true
			var data struct {
				Runs  []githubCheck `json:"check_runs"`
				Total int           `json:"total_count"`
			}
			next, err := s.get(ctx, path, &data)
			if err != nil {
				out.Error = "Some current-head checks are unavailable"
				break
			}
			readRuns += len(data.Runs)
			totalRuns = max(totalRuns, data.Total)
			for _, v := range data.Runs {
				if v.SHA != pr.Head.SHA || v.Name == "" {
					out.Error = "Check identity does not match current head"
					continue
				}
				key := repo + ":" + fmt.Sprint(v.App.ID) + ":" + v.Name
				if old, ok := checks[key]; !ok || v.ID > old.ID {
					checks[key] = v
				}
			}
			path = next
		}
		if readRuns < totalRuns {
			out.Error = "CI pagination incomplete"
		}
		items, err := githubPages[githubStatus](ctx, s, fmt.Sprintf("/repos/%s/commits/%s/statuses?per_page=100", repo, pr.Head.SHA))
		if err != nil {
			out.Error = "Some current-head commit statuses are unavailable"
			continue
		}
		for _, v := range items {
			key := repo + ":" + v.Context
			if old, ok := statuses[key]; !ok || v.CreatedAt > old.CreatedAt || v.CreatedAt == old.CreatedAt && v.ID > old.ID {
				statuses[key] = v
			}
		}
	}
	return checks, statuses
}

func validGitHubRepository(repo string) bool {
	parts := strings.Split(repo, "/")
	return len(parts) == 2 && githubName.MatchString(parts[0]) && githubName.MatchString(parts[1]) && parts[0] != "." && parts[0] != ".." && parts[1] != "." && parts[1] != ".."
}
