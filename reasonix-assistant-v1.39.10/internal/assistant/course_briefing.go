package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reasonix/internal/heartbeat"
	"slices"
	"strconv"
)

const CourseBriefingInstructions = `为Sir播报宿主日期的今日课程，必须实际调用assistant_courses action=query并明确传入宿主给出的北京时间自然日。使用返回的教学周、课程时间和教室，不从聊天记忆猜课表。连上的同一门课程作为一段课程呈现。无课时简短说明今天无课；没有配置或读取失败不能冒充无课。正常课程只报告必要课程事实。不要创建待办、改作息或课表。最终只返回JSON：{"notify":true,"message":"简短中文正文，称呼Sir","reason":"简短依据","course_ids":["本次查询返回的全部课程实例id，保持原顺序"]}。`

func courseReadID(job string, attempt int64) string {
	return mailHash(job + ":" + strconv.FormatInt(attempt, 10))
}
func (s CourseService) recordRead(ctx context.Context, day CourseDay) error {
	id := os.Getenv("REASONIX_ASSISTANT_JOB_ID")
	attempt, e := strconv.ParseInt(os.Getenv("REASONIX_ASSISTANT_COURSE_ATTEMPT"), 10, 64)
	if id == "" || e != nil || attempt <= 0 {
		return errors.New("course background identity missing")
	}
	return NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		return saveEntity(ctx, tx, "course_read_receipt", courseReadID(id, attempt), day)
	})
}
func loadCourseRead(ctx context.Context, tx *sql.Tx, t heartbeat.Task) (CourseDay, error) {
	attempt := t.RunStartedAt
	if attempt == 0 && len(t.History) > 0 {
		attempt = t.History[len(t.History)-1].StartedAt
	}
	var raw string
	var d CourseDay
	if err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='course_read_receipt' AND id=?", courseReadID(t.ID, attempt)).Scan(&raw); err != nil {
		return d, err
	}
	return d, json.Unmarshal([]byte(raw), &d)
}
func (s CourseService) VerifyBriefing(ctx context.Context, t heartbeat.Task, raw string) error {
	var v struct {
		Notify  bool     `json:"notify"`
		Message string   `json:"message"`
		IDs     []string `json:"course_ids"`
	}
	if json.Unmarshal([]byte(raw), &v) != nil || !v.Notify || v.Message == "" || v.IDs == nil {
		return errors.New("course briefing requires notify, message and course_ids")
	}
	return NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		d, e := loadCourseRead(ctx, tx, t)
		if e != nil {
			return errors.New("course briefing must query actual timetable")
		}
		at := t.ScheduledTime(s.now())
		if d.Date != at.In(mustShanghai()).Format("2006-01-02") {
			return errors.New("course briefing queried the wrong civil date")
		}
		ids := []string{}
		for _, c := range d.Courses {
			ids = append(ids, c.ID)
		}
		if !slices.Equal(ids, v.IDs) {
			return errors.New("course briefing IDs do not match timetable read")
		}
		return nil
	})
}

func (s CourseService) briefingCurrent(ctx context.Context, t heartbeat.Task) (bool, error) {
	if len(t.History) == 0 || t.History[len(t.History)-1].Status != "delivery_pending" {
		return true, nil
	}
	err := NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		receipt, e := loadCourseRead(ctx, tx, t)
		if e != nil {
			return e
		}
		current, e := loadCourseConfig(ctx, tx)
		if e != nil {
			return e
		}
		if receipt.Revision != current.Revision {
			return heartbeat.ErrOccurrenceChanged
		}
		return nil
	})
	return err == nil, err
}
