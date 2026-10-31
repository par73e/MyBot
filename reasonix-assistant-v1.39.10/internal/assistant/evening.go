package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"reasonix/internal/heartbeat"
)

const EveningInstructions = `为Sir生成23:00睡前小结。必须实际调用assistant_evening action=snapshot读取本次真实快照；宿主负责把剩余事项与明日最早安排渲染成简短正文，直接从有用的提醒说起，没有待办就省略待办一行，不加固定标题或开场晚安。你只写末尾一句简短、自然的关照，像平时晚上聊天时顺口说的话。每次根据当前有效长短期记忆、会话与当晚真实安排自行组织表达，自然选择措辞和句式。没有固定范文或句库，也不额外查询历史结尾来凑差异；不要为了求变化编造事情。语气克制，不说教、不命令用户放下手头的事，不自称Friday或承诺接着盯、替用户操心等额外服务。可以在结尾自然说晚安，不必每次用同样的句式，也不必凑“夜深了”“今天辛苦了”等开场。不要在care中增加任何行程、时间、完成情况、地点、健康、正在忙或已经睡着等未经提供的事实。宿主已称呼Sir，care无需重复称呼。不要调天气、邮箱、新闻，不修改任何个人状态。数据缺失不能假装无事。最终严格只返回一个JSON对象，唯一字段care为40字符以内无换行的自然关照句。`

type EveningSnapshot struct {
	ID                 string           `json:"id"`
	Date               string           `json:"date"`
	AsOf               time.Time        `json:"as_of"`
	Tasks              []Todo           `json:"remaining_tasks"`
	CourseStatus       string           `json:"tomorrow_course_status"`
	Courses            []CourseInstance `json:"tomorrow_courses"`
	TomorrowReminder   string           `json:"tomorrow_first_reminder,omitempty"`
	TomorrowReminderAt time.Time        `json:"tomorrow_first_reminder_at,omitempty"`
}
type EveningService struct {
	Home  string
	Clock func() time.Time
}

func (s EveningService) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}
func (s EveningService) facts(ctx context.Context, id string) (EveningSnapshot, error) {
	now := s.now().In(mustShanghai())
	date := now.Format("2006-01-02")
	v := EveningSnapshot{ID: id, Date: date, AsOf: now, Tasks: []Todo{}, Courses: []CourseInstance{}, CourseStatus: "unavailable"}
	views, e := (Tasks{Repository: NewRepository(s.Home)}).List(ctx, false)
	if e != nil {
		return v, e
	}
	tomorrow := now.AddDate(0, 0, 1)
	tomorrowDate := tomorrow.Format("2006-01-02")
	routine, e := (HeartbeatService{Home: s.Home}).Config(ctx)
	if e != nil {
		return v, e
	}
	wake, end, open := routine.Bounds(tomorrowDate)
	for _, t := range views {
		if t.Status != "open" {
			continue
		}
		due, _ := timestamp(t.DueAt)
		if t.Kind != "reminder" && (t.PlannedFor == date || !due.IsZero() && due.In(mustShanghai()).Format("2006-01-02") <= date || t.Schedule != nil && t.Schedule.ScheduledOn(now)) {
			v.Tasks = append(v.Tasks, t.Todo)
		}
		if t.Schedule != nil && t.Schedule.Enabled && t.Schedule.At != "" {
			at, _ := timestamp(t.Schedule.At)
			if open && !at.Before(wake) && at.Before(end) && at.In(mustShanghai()).Format("2006-01-02") == tomorrowDate && (v.TomorrowReminderAt.IsZero() || at.Before(v.TomorrowReminderAt)) {
				v.TomorrowReminderAt = at
				v.TomorrowReminder = t.Title
			}
		}
	}
	c, e := (CourseService{Home: s.Home}).Config(ctx)
	if e == nil {
		v.CourseStatus = "ok"
		v.Courses = c.Day(tomorrow).Courses
	} else if !errors.Is(e, sql.ErrNoRows) {
		return v, e
	}
	return v, nil
}
func (s EveningService) Prepare(ctx context.Context, t heartbeat.Task) error {
	v, e := s.facts(ctx, t.ID)
	if e != nil {
		return e
	}
	return NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		return saveEntity(ctx, tx, "evening_snapshot", courseReadID(t.ID, t.RunStartedAt), v)
	})
}
func (s EveningService) Finalize(ctx context.Context, t heartbeat.Task, raw string, fallback bool) (string, error) {
	var out struct {
		Care string `json:"care"`
	}
	e := decode([]byte(raw), &out)
	if e != nil && !fallback {
		return "", e
	}
	care := strings.TrimSpace(out.Care)
	if care == "" || utf8.RuneCountInString(care) > 40 || strings.ContainsAny(care, "\r\n0123456789") || strings.Contains(care, "已完成") || strings.Contains(care, "到校") {
		if !fallback {
			return "", errors.New("evening care must be a short, factual-neutral sentence")
		}
		care = "早点休息吧，晚安。"
	}
	if !fallback {
		e = NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
			var proof string
			if e := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='evening_read' AND id=?", courseReadID(t.ID, t.RunStartedAt)).Scan(&proof); e != nil {
				return e
			}
			if proof != "true" {
				return errors.New("evening snapshot was not read")
			}
			return nil
		})
		if e != nil {
			return "", e
		}
	}
	v, e := s.facts(ctx, t.ID)
	if e != nil {
		return "", e
	}
	message := renderEvening(v, care)
	e = NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		return saveEntity(ctx, tx, "evening_result", courseReadID(t.ID, t.RunStartedAt), map[string]any{"snapshot": v, "message": message, "fallback": fallback})
	})
	return message, e
}
func renderEvening(v EveningSnapshot, care string) string {
	lines := []string{}
	if len(v.Tasks) > 0 {
		names := []string{}
		for _, t := range v.Tasks[:min(3, len(v.Tasks))] {
			name := eveningLabel(t.Title, 14)
			if d, e := timestamp(t.DueAt); e == nil && !d.IsZero() && d.Before(v.AsOf) {
				name += "（已逾期）"
			}
			names = append(names, name)
		}
		remaining := "今天还有" + strings.Join(names, "、") + "没处理"
		if len(v.Tasks) > 3 {
			remaining += fmt.Sprintf("，另有%d项", len(v.Tasks)-3)
		}
		remaining += "。"
		lines = append(lines, remaining)
	}
	tomorrow := "明天的课表暂时没查到。"
	if v.CourseStatus == "ok" {
		tomorrow = "明天没有课。"
		if len(v.Courses) > 0 {
			c := v.Courses[0]
			tomorrow = fmt.Sprintf("明天最早%s有%s的课，%s。", c.StartsAt.Format("15:04"), eveningLabel(c.Title, 20), eveningLabel(c.Room, 16))
		}
	}
	if !v.TomorrowReminderAt.IsZero() && (len(v.Courses) == 0 || v.TomorrowReminderAt.Before(v.Courses[0].StartsAt)) {
		tomorrow = fmt.Sprintf("明天%s会提醒您：%s。", v.TomorrowReminderAt.In(mustShanghai()).Format("15:04"), eveningLabel(v.TomorrowReminder, 24))
	}
	lines = append(lines, tomorrow, care)
	lines[0] = addressPrefix() + lines[0]
	return strings.Join(lines, "\n")
}
func (s EveningService) Acknowledge(ctx context.Context, t heartbeat.Task, r heartbeat.Run) error {
	if t.ModuleID != "evening_briefing" {
		return nil
	}
	return NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		var raw string
		if e := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='evening_result' AND id=?", courseReadID(t.ID, r.StartedAt)).Scan(&raw); e != nil {
			return e
		}
		return saveEntity(ctx, tx, "evening_delivered", t.ID, json.RawMessage(raw))
	})
}

func eveningLabel(text string, limit int) string {
	return singleLine(text)
}
