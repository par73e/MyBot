package assistant

import (
	"sort"
	"time"
)

// Resolve the teaching week from the source date but apply personal exceptions
// and occurrence identities to the real attendance date.
func (c CourseConfig) academicDay(day time.Time) CourseDay {
	a := c.academic.Day(day)
	date := day.In(mustShanghai()).Format("2006-01-02")
	if !a.Known {
		return c.rawDay(day)
	}
	actual, _ := courseDate(date)
	result := CourseDay{Configured: true, Revision: c.Revision, Date: date, Week: c.Week(day), InTerm: a.Term != "", Courses: []CourseInstance{}, Academic: &a}
	source, _ := courseDate(a.ClassDate)
	for _, def := range c.Courses {
		if a.Teaching && c.matches(def, source) {
			if v, ok := c.instance(def, actual); ok && v.Date == date {
				v.Week = c.Week(source)
				result.Courses = append(result.Courses, v)
			}
		}
		// An explicit moved-in class remains present even on a school-wide rest day.
		for _, ex := range c.Exceptions {
			if ex.CourseID != def.ID || ex.MoveTo != date || ex.Date == date {
				continue
			}
			original, _ := courseDate(ex.Date)
			teachingSource := original
			originalDay := c.academic.Day(original)
			if originalDay.Teaching && originalDay.ClassDate != "" {
				teachingSource, _ = courseDate(originalDay.ClassDate)
			}
			if !c.matches(def, teachingSource) {
				continue
			}
			if v, ok := c.instance(def, original); ok {
				v.Week = c.Week(teachingSource)
				result.Courses = append(result.Courses, v)
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
