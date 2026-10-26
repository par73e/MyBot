package assistant

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"
)

var alertAreas = []string{"浦东新区", "黄浦区", "静安区", "徐汇区", "长宁区", "普陀区", "虹口区", "杨浦区", "宝山区", "闵行区", "嘉定区", "金山区", "松江区", "青浦区", "奉贤区", "崇明区"}
var alertHazards = []string{"道路结冰", "雷雨大风", "台风", "暴雨", "暴雪", "寒潮", "大风", "低温", "霜冻", "高温", "雷电", "冰雹", "大雾", "霾"}
var alertBodyTime = regexp.MustCompile(`(20\d{2})年(\d{1,2})月(\d{1,2})日(\d{1,2})[时:](\d{1,2})分?`)
var alertFile = regexp.MustCompile(`^\d{5,12}-\d{14}-\d{4}\.html$`)
var alertCancelPattern = regexp.MustCompile(`(?:解除|取消)[“「\s]*(?:道路结冰|雷雨大风|台风|暴雨|暴雪|寒潮|大风|低温|霜冻|高温|雷电|冰雹|大雾|霾)(?:蓝色|黄色|橙色|红色)?预警`)
var alertIssuerCancelPattern = regexp.MustCompile(`^[^。；：:]{0,24}气象台[0-9年月日时分秒\s:：于]*(?:发布[:：]?)?(?:解除|取消)[“「\s]*(?:道路结冰|雷雨大风|台风|暴雨|暴雪|寒潮|大风|低温|霜冻|高温|雷电|冰雹|大雾|霾)(?:蓝色|黄色|橙色|红色)?预警`)
var alertNMCID = regexp.MustCompile(`^31\d{12}_\d{14}$`)

func alertTime(raw string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006/01/02 15:04", "20060102150405", "200601021504", "2006-01-02 15:04", "2006年1月2日15时4分"} {
		if v, e := time.ParseInLocation(layout, raw, mustShanghai()); e == nil {
			return v, nil
		}
	}
	return time.Time{}, errors.New("official alert publication time is missing or invalid")
}
func canonicalAlert(a OfficialAlert, title string, now time.Time) (OfficialAlert, error) {
	if a.Area == "" {
		a.Area = alertArea(title, a.Body)
	}
	if a.Area == "" {
		return a, errors.New("official alert Shanghai coverage is ambiguous")
	}
	if a.Hazard == "" {
		for _, v := range alertHazards {
			if strings.Contains(title, v) {
				a.Hazard = v
				break
			}
		}
	}
	if a.Level == "" {
		for _, v := range []string{"红色", "橙色", "黄色", "蓝色"} {
			if strings.Contains(title, v) {
				a.Level = v
				break
			}
		}
	}
	a.Rank = map[string]int{"蓝色": 1, "黄色": 2, "橙色": 3, "红色": 4}[a.Level]
	if a.Rank == 0 || !containsAlertHazard(a.Hazard) || a.PublishedAt.IsZero() || a.PublishedAt.After(now.Add(5*time.Minute)) || a.Identifier == "" {
		return a, errors.New("official alert has invalid type, level, identifier or time")
	}
	a.Body = cleanAlertText(a.Body, 4000)
	if match := alertBodyTime.FindString(a.Body); match != "" {
		if v, e := alertTime(match); e == nil && !v.After(now.Add(5*time.Minute)) && !v.Before(a.PublishedAt.Add(-24*time.Hour)) {
			a.PublishedAt = v
		}
	}
	a.Key = mailHash(a.Area + ":" + a.Hazard)
	return a, nil
}
func containsAlertHazard(v string) bool { return slices.Contains(alertHazards, v) }
func alertArea(title, body string) string {
	for _, v := range alertAreas {
		// Official town fields sometimes omit 区. Never match just 黄浦/浦东
		// in an arbitrary defensive-guide paragraph.
		short := strings.TrimSuffix(v, "区")
		if v == "浦东新区" {
			short = "浦东"
		}
		if strings.Contains(title, v) || strings.TrimSpace(title) == short || strings.HasPrefix(body, short+"气象台") || strings.HasPrefix(body, v+"气象台") {
			return v
		}
	}
	if strings.Contains(title, "中心城区") {
		return "中心城区"
	}
	if strings.Contains(title, "上海") || strings.HasPrefix(body, "上海市气象台") || strings.HasPrefix(body, "上海中心气象台") {
		return "全市"
	}
	return ""
}
func cleanAlertText(v string, limit int) string {
	v = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, v)
	v = strings.Join(strings.Fields(v), " ")
	rr := []rune(v)
	if len(rr) > limit {
		return string(rr[:limit]) + "…"
	}
	return v
}
func nmcAlertBody(raw []byte) (string, error) {
	doc, err := html.Parse(strings.NewReader(string(raw)))
	if err != nil {
		return "", err
	}
	parts := []string{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && nodeAttr(n, "id") == "alarmtext" {
			parts = append(parts, nodeText(n))
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return "", errors.New("official alert detail has no alert text")
	}
	return cleanAlertText(strings.Join(parts, "\n"), 4000), nil
}
func explicitAlertCancel(title, body string) bool {
	// A list disappearance or a generic defence-guide reference to cancellation
	// is never a cancellation. Only an issuer's leading cancellation statement.
	head := []rune(body)
	if len(head) > 180 {
		head = head[:180]
	}
	return alertCancelPattern.MatchString(title) || alertIssuerCancelPattern.MatchString(string(head))
}
