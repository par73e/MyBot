package assistant

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

func courseDate(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", s, mustShanghai())
}
func courseClock(date, clock string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02 15:04", date+" "+clock, mustShanghai())
}
func (c CourseConfig) Week(date time.Time) int {
	first, err := courseDate(c.FirstWeekMonday)
	if err != nil {
		return 0
	}
	day := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, mustShanghai())
	if day.Before(first) {
		return 0
	}
	return int(day.Sub(first)/(7*24*time.Hour)) + 1
}
func (c CourseConfig) Validate() error {
	first, err := courseDate(c.FirstWeekMonday)
	if err != nil || first.Weekday() != time.Monday || c.Timezone != "Asia/Shanghai" || c.TotalWeeks < 1 || c.TotalWeeks > 60 || c.Revision < 1 || strings.TrimSpace(c.Name) == "" {
		return errors.New("valid term name, Monday anchor, weeks 1..60 and Asia/Shanghai required")
	}
	if len(c.Periods) == 0 || len(c.Periods) > 30 || len(c.Courses) > 500 || len(c.Exceptions) > 1000 {
		return errors.New("invalid timetable capacity")
	}
	periods := map[int]CoursePeriod{}
	for _, p := range c.Periods {
		a, e1 := courseClock(c.FirstWeekMonday, p.Start)
		b, e2 := courseClock(c.FirstWeekMonday, p.End)
		if p.Number < 1 || p.Number > 30 || e1 != nil || e2 != nil || !a.Before(b) {
			return errors.New("invalid course period")
		}
		if _, ok := periods[p.Number]; ok {
			return errors.New("duplicate period number")
		}
		periods[p.Number] = p
	}
	ids := map[string]CourseDefinition{}
	for _, v := range c.Courses {
		if err := validateCourse(v, c.TotalWeeks, periods); err != nil {
			return err
		}
		if _, ok := ids[v.ID]; ok {
			return errors.New("duplicate course ID")
		}
		ids[v.ID] = v
	}
	seen := map[string]bool{}
	for _, ex := range c.Exceptions {
		if err := c.validateException(ex, ids); err != nil {
			return err
		}
		key := ex.CourseID + ":" + ex.Date
		if seen[key] {
			return errors.New("duplicate occurrence exception")
		}
		seen[key] = true
	}
	return nil
}
func validateCourse(v CourseDefinition, weeks int, periods map[int]CoursePeriod) error {
	if v.ID == "" || len(v.ID) > 80 || strings.ContainsAny(v.ID, " /\\:") || strings.TrimSpace(v.Title) == "" || len(v.Title) > 512 || len(v.Room) > 512 || len(v.Teacher) > 256 || v.Weekday < 1 || v.Weekday > 7 || len(v.Periods) == 0 {
		return errors.New("invalid course identity, weekday or periods")
	}
	lastEnd := ""
	last := 0
	for _, n := range v.Periods {
		p, ok := periods[n]
		if !ok || n <= last || last != 0 && n != last+1 || lastEnd > p.Start {
			return errors.New("course periods must be ordered, consecutive and non-overlapping")
		}
		last = n
		lastEnd = p.End
	}
	seen := map[int]bool{}
	for _, w := range v.Weeks {
		if w < 1 || w > weeks || seen[w] {
			return errors.New("invalid teaching weeks")
		}
		seen[w] = true
	}
	return nil
}
func (c CourseConfig) validateException(ex CourseException, ids map[string]CourseDefinition) error {
	v, ok := ids[ex.CourseID]
	if !ok {
		return errors.New("unknown exception course")
	}
	day, err := courseDate(ex.Date)
	if err != nil || !c.matches(v, day) {
		return errors.New("exception must target an actual original occurrence")
	}
	if ex.Cancelled && (ex.MoveTo != "" || ex.Start != "" || ex.End != "" || ex.Room != "") {
		return errors.New("cancelled occurrence cannot also be moved")
	}
	target := ex.Date
	if ex.MoveTo != "" {
		d, e := courseDate(ex.MoveTo)
		if e != nil || c.Week(d) < 1 || c.Week(d) > c.TotalWeeks {
			return errors.New("moved date must be inside term")
		}
		target = ex.MoveTo
	}
	if (ex.Start == "") != (ex.End == "") {
		return errors.New("both exception start and end are required")
	}
	if ex.Start != "" {
		a, e1 := courseClock(target, ex.Start)
		b, e2 := courseClock(target, ex.End)
		if e1 != nil || e2 != nil || !a.Before(b) {
			return errors.New("invalid exception times")
		}
	}
	if len(ex.Room) > 512 {
		return fmt.Errorf("exception room exceeds limit")
	}
	return nil
}
func (c CourseConfig) matches(v CourseDefinition, day time.Time) bool {
	week := c.Week(day)
	weekday := (int(day.Weekday())+6)%7 + 1
	return week >= 1 && week <= c.TotalWeeks && weekday == v.Weekday && (len(v.Weeks) == 0 || slices.Contains(v.Weeks, week))
}
