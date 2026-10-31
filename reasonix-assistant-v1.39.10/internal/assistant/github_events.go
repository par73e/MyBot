package assistant

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var githubHTMLComment = regexp.MustCompile(`(?s)<!--.*?-->`)

func githubExcerpt(text string) string {
	// Notification text is quoted data, not a tool instruction or rendered HTML.
	text = githubHTMLComment.ReplaceAllString(text, "")
	text = strings.NewReplacer("`", "", "[", "（", "]", "）", "<", "〈", ">", "〉", "*", "", "#", "").Replace(text)
	return boundedNewsText(text, 100)
}

func (s GitHubService) events(w GitHubWatch, next GitHubSnapshot, login string) []GitHubEvent {
	if w.Baseline == nil {
		return nil
	}
	old := w.Baseline
	events := []GitHubEvent{}
	add := func(kind, key, text, link, state, sha string) {
		events = append(events, s.githubEvent(w, kind, key, text, githubPublicLink(link, next.URL), state, sha))
	}
	if old.State != next.State {
		text := map[string]string{"merged": "PR已合并。", "closed": "PR已关闭，尚未合并。", "open": "PR已重新打开。"}[next.State]
		if text != "" {
			add("state", old.State+":"+next.State+":"+next.HeadSHA, text, next.URL, next.State, "")
		}
	}
	events = append(events, s.discussionEvents(w, next, login)...)
	if next.CI.State == "failure" && (w.FailedCIHead != next.HeadSHA || strings.Join(w.FailedNames, "\x00") != strings.Join(next.CI.Failed, "\x00")) {
		add("ci", next.HeadSHA+":failure:"+strings.Join(next.CI.Failed, ","), "提交"+next.HeadSHA[:7]+"的CI检查失败："+githubExcerpt(strings.Join(next.CI.Failed, "、"))+"。", next.URL, "failure", next.HeadSHA)
	}
	if w.FailedCIHead == next.HeadSHA && next.CI.State == "success" {
		add("ci", next.HeadSHA+":recovered", "提交"+next.HeadSHA[:7]+"此前失败的CI已恢复通过。", next.URL, "success", next.HeadSHA)
	}
	return events
}

func renderGitHub(snap GitHubSnapshot, events []GitHubEvent) string {
	lines := []string{fmt.Sprintf("%s%s #%d 有新进展。", addressPrefix(), snap.Repository, snap.Number)}
	for _, e := range events[:min(5, len(events))] {
		if githubDiscussion(e.Kind) {
			lines = append(lines, githubDiscussionLabel(e)+"，中文摘要暂不可用，请查看原文。[原文]("+e.URL+")")
		} else {
			lines = append(lines, e.Text)
		}
	}
	if len(events) > 5 {
		lines = append(lines, fmt.Sprintf("另有%d项变化，可打开PR查看。", len(events)-5))
	}
	lines = append(lines, "[查看 PR]("+snap.URL+")")
	return strings.Join(lines, "\n")
}

func (s GitHubService) githubEvent(w GitHubWatch, kind, key, text, link, state, sha string) GitHubEvent {
	return GitHubEvent{ID: "gh-event-" + mailHash(w.ID+":"+fmt.Sprint(w.Revision)+":"+kind+":"+key), WatchID: w.ID, Kind: kind, Text: text, URL: link, State: state, HeadSHA: sha, ObservedAt: s.now()}
}

func latestGitHubReviews(next GitHubSnapshot) map[string]GitHubItem {
	latest := map[string]GitHubItem{}
	for _, v := range next.Items {
		if v.Kind == "review" && v.State != "PENDING" {
			if old, ok := latest[strings.ToLower(v.Author)]; !ok || v.UpdatedAt > old.UpdatedAt || v.UpdatedAt == old.UpdatedAt && v.Sequence > old.Sequence {
				latest[strings.ToLower(v.Author)] = v
			}
		}
	}
	return latest
}

func (s GitHubService) discussionEvents(w GitHubWatch, next GitHubSnapshot, login string) []GitHubEvent {
	events := []GitHubEvent{}
	latest := latestGitHubReviews(next)
	keys := make([]string, 0, len(next.Items))
	for k := range next.Items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		v := next.Items[key]
		previous, existed := w.Baseline.Items[key]
		if existed && previous.Fingerprint == v.Fingerprint {
			continue
		}
		if strings.EqualFold(v.Author, login) || v.Bot && !v.MentionsOwner || v.State == "PENDING" {
			continue
		}
		if v.Kind == "review" && latest[strings.ToLower(v.Author)].ID != v.ID {
			continue
		}
		label := githubItemLabel(v, previous, existed, next.HeadSHA)
		if label == "" {
			continue
		}
		text := v.Author + label
		if body := githubExcerpt(v.Text); body != "" {
			text += "：「" + body + "」"
		}
		event := s.githubEvent(w, v.Kind, v.ID+":"+v.Fingerprint, text+"。", v.URL, v.State, v.CommitSHA)
		event.SourceID = v.ID
		event.Fingerprint = v.Fingerprint
		event.Author, event.Label = v.Author, label
		event.Body = v.Body
		event.BodyTruncated = v.BodyTruncated
		if event.Body == "" {
			event.Body, event.BodyTruncated = v.Text, v.TextTruncated
		}
		events = append(events, event)
	}
	return events
}

func githubItemLabel(v, previous GitHubItem, existed bool, head string) string {
	label := "有新回复"
	if existed {
		label = "修改了回复"
	}
	if v.Kind == "inline" {
		label = "留下了代码审查意见"
		if existed {
			label = "修改了代码审查意见"
		}
	}
	if v.Kind != "review" {
		return label
	}
	label = map[string]string{"APPROVED": "通过了审核", "CHANGES_REQUESTED": "要求修改", "DISMISSED": "的审核已被撤销", "COMMENTED": "提交了审核意见"}[v.State]
	if label == "" {
		return ""
	}
	if existed && previous.State == v.State {
		label = "修改了审核意见"
	}
	if v.CommitSHA != "" && v.CommitSHA != head {
		label += "（针对提交" + boundedNewsText(v.CommitSHA, 7) + "）"
	}
	return label
}
