package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reasonix/internal/heartbeat"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (s MorningService) Finalize(ctx context.Context, t heartbeat.Task, raw string) (string, error) {
	var v MorningOutput
	if e := json.Unmarshal([]byte(raw), &v); e != nil {
		return "", e
	}
	snap, e := s.Snapshot(ctx, t.ID)
	if e != nil {
		return "", e
	}
	if e = s.verify(ctx, t, snap, v); e != nil {
		return "", e
	}
	anyNotify := false
	for _, d := range v.MailDecisions {
		anyNotify = anyNotify || *d.Notify
	}
	decisions, _ := json.Marshal(map[string]any{"notify": anyNotify, "mail_decisions": v.MailDecisions})
	if e = NewMailService(s.Home).RecordMailDecisions(ctx, t, string(decisions)); e != nil {
		return "", e
	}
	weatherRef, err := s.weatherReference(ctx, t)
	if err != nil && snap.WeatherConfigured {
		return "", err
	}
	if e = s.refreshFacts(ctx, &snap); e != nil {
		return "", e
	}
	message := renderMorning(snap, v)
	questions, e := (Proposals{Home: s.Home}).questions(ctx, t, 1)
	if e != nil {
		return "", e
	}
	if len(questions) > 0 {
		lines := strings.Split(message, "\n")
		lines[3] = strings.TrimSuffix(lines[3], "。") + "；" + questions[0]
		message = strings.Join(lines, "\n")
	}
	displayed := []string{}
	for _, d := range v.MailDecisions {
		if *d.Notify {
			displayed = append(displayed, d.ID)
		}
	}
	result := map[string]any{"snapshot": snap, "output": v, "message": message, "displayed_mail_ids": displayed, "weather_reference": weatherRef}
	e = NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		return saveEntity(ctx, tx, "morning_result", courseReadID(t.ID, t.RunStartedAt), result)
	})
	return message, e
}
func (s MorningService) verify(ctx context.Context, t heartbeat.Task, snap MorningSnapshot, v MorningOutput) error {
	if e := validateMorningWeather(v.Weather); e != nil {
		return e
	}
	if v.MailDecisions == nil || v.News == nil || len(v.MailDecisions) != len(snap.MailIDs) || len(v.News) != len(snap.News.Items) {
		return errors.New("morning result omitted source items")
	}
	if e := NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		var raw string
		e := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='morning_read_receipt' AND id=?", courseReadID(t.ID, t.RunStartedAt)).Scan(&raw)
		if e != nil || raw != "true" {
			return errors.New("morning did not read host snapshot")
		}
		return nil
	}); e != nil {
		return e
	}
	if e := NewMailService(s.Home).VerifyMailReads(ctx, t.ID, t.RunStartedAt, snap.MailIDs); e != nil {
		return e
	}
	if snap.WeatherConfigured {
		if e := NewWeatherService(s.Home).VerifyWeatherRead(ctx, t.ID, t.RunStartedAt); e != nil {
			return e
		}
	}
	allowed := map[string]bool{}
	for _, id := range snap.MailIDs {
		allowed[id] = true
	}
	for i, d := range v.MailDecisions {
		if !allowed[d.ID] || d.Notify == nil {
			return fmt.Errorf("mail_decisions[%d] has an invalid/duplicate id or missing notify", i)
		}
		if strings.TrimSpace(d.Summary) == "" || utf8.RuneCountInString(d.Summary) > 40 || strings.ContainsAny(d.Summary, "\r\n") {
			return fmt.Errorf("mail_decisions[%d].summary has %d characters; use 1..40 characters without newlines", i, utf8.RuneCountInString(d.Summary))
		}
		if strings.TrimSpace(d.Reason) == "" || len(d.Reason) > 2000 {
			return fmt.Errorf("mail_decisions[%d].reason must be nonempty and at most 2000 bytes", i)
		}
		delete(allowed, d.ID)
	}
	if e := validateMorningNews(snap, v.News); e != nil {
		return e
	}
	return NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		for i, d := range v.MailDecisions {
			if e := validateTaskCandidates(ctx, tx, t, d); e != nil {
				return fmt.Errorf("mail_decisions[%d]: %w", i, e)
			}
		}
		return nil
	})
}

func validateMorningWeather(text string) error {
	if strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 100 || strings.ContainsAny(text, "\r\n") || strings.Count(text, "。") > 1 {
		return errors.New("morning weather must be one concise sentence of at most 100 characters")
	}
	return nil
}
func validateMorningNews(snap MorningSnapshot, news []MorningNews) error {
	if news == nil || len(news) != len(snap.News.Items) {
		return errors.New("morning news must cover the host items")
	}
	for i, n := range news {
		if n.ID != snap.News.Items[i].ID || strings.TrimSpace(n.Summary) == "" || utf8.RuneCountInString(n.Summary) > 25 || strings.ContainsAny(n.Summary, "\r\n") || strings.Count(n.Summary, "。") > 1 {
			return errors.New("morning news must match the host IDs and use one sentence of at most 25 characters each")
		}
	}
	return nil
}

func renderMorning(s MorningSnapshot, v MorningOutput) string {
	weather := v.Weather
	if !s.WeatherConfigured || weather == "暂未获取" {
		weather = "今天的天气暂时没查到"
	}
	mails := []string{}
	for _, d := range v.MailDecisions {
		if *d.Notify {
			mails = append(mails, d.Summary)
		}
	}
	mailText := "昨晚没有需要特别提醒的邮件"
	if len(s.MailIDs) == 0 && s.RemainingMail == 0 && !s.OvernightMailSeen {
		mailText = "昨天晚上没有新邮件"
	}
	if len(mails) > 0 {
		mailText = "收到的新邮件里，" + strings.Join(mails, "；")
	}
	if s.MailStatus != "ok" {
		if len(mails) == 0 {
			mailText = "有些邮箱暂时没查到，稍后再看看"
		} else {
			mailText += "，还有些邮箱暂时没查到"
		}
	}
	if s.MailStatus == "unavailable" && len(mails) == 0 {
		mailText = "邮件暂时没查到，稍后再看看"
	}
	if s.MailStatus == "paused" {
		mailText = "邮件自动检查还暂停着"
	}
	if s.RemainingMail > 0 {
		mailText += fmt.Sprintf("；另%d封还没看完", s.RemainingMail)
	}
	todos := []string{}
	for _, t := range s.Todos[:min(3, len(s.Todos))] {
		title := singleLine(t.Title)
		runes := []rune(title)
		if len(runes) > 20 {
			title = string(runes[:20]) + "…"
		}
		if due, e := time.Parse(time.RFC3339, t.DueAt); e == nil && due.In(mustShanghai()).Format("2006-01-02") < s.Date {
			title += "（逾期）"
		}
		todos = append(todos, title)
	}
	if len(s.Todos) > 3 {
		todos = append(todos, fmt.Sprintf("另%d项", len(s.Todos)-3))
	}
	todoText := "今天没有要处理的待办"
	if len(todos) > 0 {
		todoText = "今天记得" + strings.Join(todos, "、")
	}
	news := []string{}
	for _, n := range v.News {
		news = append(news, n.Summary)
	}
	newsText := "过去24小时暂时没找到新的AI消息"
	if len(news) > 0 {
		newsText = fmt.Sprintf("AI圈有%d条消息，%s", len(news), strings.Join(news, "；"))
	} else if s.News.Status == "unavailable" {
		newsText = "AI新闻暂时没查到，稍后再看看"
	}
	lines := []string{"# **Sir，早上好。**"}
	for _, line := range []string{weather, morningCourses(s), mailText, todoText, newsText} {
		lines = append(lines, morningSentence(line))
	}
	return strings.Join(lines, "\n")
}

func morningSentence(s string) string {
	s = singleLine(s)
	s = strings.NewReplacer("。", "；", "！", "；", "？", "；").Replace(s)
	return strings.TrimRight(s, "；;，, .!?！？") + "。"
}
func morningCourses(s MorningSnapshot) string {
	if s.CourseStatus != "ok" {
		return "今天的课表暂时没查到"
	}
	if len(s.Courses.Courses) == 0 {
		return "今天没有课"
	}
	groups := [][]string{{}, {}, {}}
	for _, c := range s.Courses.Courses {
		i := 0
		if c.StartsAt.Hour() >= 18 {
			i = 2
		} else if c.StartsAt.Hour() >= 12 {
			i = 1
		}
		name := singleLine(c.Title)
		for _, wake := range s.WakeCourses {
			if wake.ID == c.ID {
				name = fmt.Sprintf("%s（%s，%s）", name, c.StartsAt.Format("15:04"), singleLine(c.Room))
				break
			}
		}
		groups[i] = append(groups[i], name)
	}
	out := []string{}
	for i, names := range groups {
		if len(names) > 0 {
			out = append(out, []string{"上午", "下午", "晚上"}[i]+"有"+strings.Join(names, "、")+"的课")
		}
	}
	return "今天" + strings.Join(out, "，")
}
func (s MorningService) Acknowledge(ctx context.Context, t heartbeat.Task, r heartbeat.Run) error {
	if t.ModuleID != "morning_briefing" {
		return nil
	}
	return NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		var raw string
		if e := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='morning_result' AND id=?", courseReadID(t.ID, r.StartedAt)).Scan(&raw); e != nil {
			return e
		}
		var v any
		if e := json.Unmarshal([]byte(raw), &v); e != nil {
			return e
		}
		return saveEntity(ctx, tx, "morning_delivered", t.ID, v)
	})
}

func singleLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func mustAttempt(v string) int64 { n, _ := strconv.ParseInt(v, 10, 64); return n }
func (s MorningService) weatherReference(ctx context.Context, t heartbeat.Task) (map[string]any, error) {
	var ref map[string]any
	err := NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		var raw string
		if e := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='morning_weather_read' AND id=?", courseReadID(t.ID, t.RunStartedAt)).Scan(&raw); e != nil {
			return e
		}
		return json.Unmarshal([]byte(raw), &ref)
	})
	if err == nil {
		snap, e := s.Snapshot(ctx, t.ID)
		if e != nil {
			return nil, e
		}
		if ref["date"] != snap.Date {
			return nil, errors.New("morning weather queried wrong civil date")
		}
	}
	return ref, err
}
