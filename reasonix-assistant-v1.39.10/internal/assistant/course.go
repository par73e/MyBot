package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

const courseConfigID = "default"

func loadCourseConfig(ctx context.Context, tx *sql.Tx) (CourseConfig, error) {
	var c CourseConfig
	var raw string
	if err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='course_config' AND id=?", courseConfigID).Scan(&raw); err != nil {
		return c, err
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return c, err
	}
	return c, c.Validate()
}
func (s CourseService) Config(ctx context.Context) (CourseConfig, error) {
	var c CourseConfig
	err := NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error { var e error; c, e = loadCourseConfig(ctx, tx); return e })
	return c, err
}
func (s CourseService) Apply(ctx context.Context, r CourseRequest) (any, error) {
	if r.Action == "query" || r.Action == "status" {
		c, err := s.Config(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return map[string]any{"configured": false, "reason": "timetable_not_configured"}, nil
		}
		if err != nil {
			return nil, err
		}
		if r.Action == "status" {
			return map[string]any{"configured": true, "config": c}, nil
		}
		date := r.Date
		if date == "" {
			date = s.now().In(mustShanghai()).Format("2006-01-02")
		}
		d, err := courseDate(date)
		if err != nil {
			return nil, err
		}
		return c.Day(d), nil
	}
	if err := allowMutation("update"); err != nil {
		return nil, err
	}
	var result CourseConfig
	err := NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		old, e := loadCourseConfig(ctx, tx)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if r.Revision != old.Revision {
			return errors.New("course revision conflict; read status first")
		}
		next := old
		switch r.Action {
		case "import":
			if r.Config == nil {
				return errors.New("config required")
			}
			next = *r.Config
		case "set_exception":
			if r.Exception == nil {
				return errors.New("exception required")
			}
			next.Exceptions = replaceCourseException(old.Exceptions, *r.Exception, false)
		case "remove_exception":
			if r.CourseID == "" || r.Date == "" {
				return errors.New("course_id and date required")
			}
			next.Exceptions = replaceCourseException(old.Exceptions, CourseException{CourseID: r.CourseID, Date: r.Date}, true)
		default:
			return errors.New("unknown course action")
		}
		next.Revision = old.Revision + 1
		if e := next.Validate(); e != nil {
			return e
		}
		if e := saveEntity(ctx, tx, "course_config", courseConfigID, next); e != nil {
			return e
		}
		result = next
		return nil
	})
	return result, err
}
func replaceCourseException(old []CourseException, ex CourseException, remove bool) []CourseException {
	out := []CourseException{}
	for _, v := range old {
		if v.CourseID != ex.CourseID || v.Date != ex.Date {
			out = append(out, v)
		}
	}
	if !remove {
		out = append(out, ex)
	}
	return out
}
func (c CourseConfig) Day(day time.Time) CourseDay {
	date := day.In(mustShanghai()).Format("2006-01-02")
	day, _ = courseDate(date)
	result := CourseDay{Configured: true, Revision: c.Revision, Date: date, Week: c.Week(day), Courses: []CourseInstance{}}
	result.InTerm = result.Week >= 1 && result.Week <= c.TotalWeeks
	if !result.InTerm {
		return result
	}
	for _, v := range c.Courses {
		if c.matches(v, day) {
			if instance, ok := c.instance(v, day); ok && instance.Date == date {
				result.Courses = append(result.Courses, instance)
			}
		}
		for _, ex := range c.Exceptions {
			if ex.CourseID == v.ID && ex.MoveTo == date && ex.Date != date {
				original, _ := courseDate(ex.Date)
				if instance, ok := c.instance(v, original); ok {
					result.Courses = append(result.Courses, instance)
				}
			}
		}
	}
	sort.Slice(result.Courses, func(i, j int) bool {
		if result.Courses[i].StartsAt.Equal(result.Courses[j].StartsAt) {
			return result.Courses[i].ID < result.Courses[j].ID
		}
		return result.Courses[i].StartsAt.Before(result.Courses[j].StartsAt)
	})
	return result
}
func (c CourseConfig) instance(v CourseDefinition, day time.Time) (CourseInstance, bool) {
	date := day.Format("2006-01-02")
	start, end := "", ""
	for _, p := range c.Periods {
		if p.Number == v.Periods[0] {
			start = p.Start
		}
		if p.Number == v.Periods[len(v.Periods)-1] {
			end = p.End
		}
	}
	x := CourseInstance{ID: v.ID + ":" + date, CourseID: v.ID, OriginalDate: date, Date: date, Week: c.Week(day), Title: v.Title, Room: v.Room, Teacher: v.Teacher, Periods: v.Periods}
	for _, ex := range c.Exceptions {
		if ex.CourseID != v.ID || ex.Date != date {
			continue
		}
		if ex.Cancelled {
			return x, false
		}
		if ex.MoveTo != "" {
			x.Date = ex.MoveTo
		}
		if ex.Start != "" {
			start, end = ex.Start, ex.End
		}
		if ex.Room != "" {
			x.Room = ex.Room
		}
	}
	actualDate, _ := courseDate(x.Date)
	x.Week = c.Week(actualDate)
	x.StartsAt, _ = courseClock(x.Date, start)
	x.EndsAt, _ = courseClock(x.Date, end)
	return x, true
}
