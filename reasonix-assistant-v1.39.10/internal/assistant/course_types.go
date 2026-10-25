package assistant

import "time"

// CourseConfig is authoritative timetable data, independent of notification clocks.
type CourseConfig struct {
	Revision        int                `json:"revision"`
	Name            string             `json:"name"`
	FirstWeekMonday string             `json:"first_week_monday"`
	TotalWeeks      int                `json:"total_weeks"`
	Timezone        string             `json:"timezone"`
	Periods         []CoursePeriod     `json:"periods"`
	Courses         []CourseDefinition `json:"courses"`
	Exceptions      []CourseException  `json:"exceptions,omitempty"`
}
type CoursePeriod struct {
	Number int    `json:"number"`
	Start  string `json:"start"`
	End    string `json:"end"`
}
type CourseDefinition struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Weekday int    `json:"weekday"` // Monday=1, Sunday=7.
	Periods []int  `json:"periods"`
	Weeks   []int  `json:"weeks"` // Explicit teaching weeks; empty means every term week.
	Room    string `json:"room"`
	Teacher string `json:"teacher,omitempty"`
}

// An exception targets the original occurrence, preserving its identity when moved.
type CourseException struct {
	CourseID  string `json:"course_id"`
	Date      string `json:"date"`
	Cancelled bool   `json:"cancelled,omitempty"`
	MoveTo    string `json:"move_to,omitempty"`
	Start     string `json:"start,omitempty"`
	End       string `json:"end,omitempty"`
	Room      string `json:"room,omitempty"`
}
type CourseInstance struct {
	ID           string    `json:"id"`
	CourseID     string    `json:"course_id"`
	OriginalDate string    `json:"original_date"`
	Date         string    `json:"date"`
	Week         int       `json:"week"`
	Title        string    `json:"title"`
	Room         string    `json:"room"`
	Teacher      string    `json:"teacher,omitempty"`
	Periods      []int     `json:"periods"`
	StartsAt     time.Time `json:"starts_at"`
	EndsAt       time.Time `json:"ends_at"`
}
type CourseDay struct {
	Configured bool             `json:"configured"`
	Revision   int              `json:"revision"`
	Date       string           `json:"date"`
	Week       int              `json:"week"`
	InTerm     bool             `json:"in_term"`
	Courses    []CourseInstance `json:"courses"`
}
type CourseRequest struct {
	Action    string           `json:"action"`
	Date      string           `json:"date,omitempty"`
	Revision  int              `json:"revision,omitempty"`
	Config    *CourseConfig    `json:"config,omitempty"`
	Exception *CourseException `json:"exception,omitempty"`
	CourseID  string           `json:"course_id,omitempty"`
}
type CourseService struct {
	Home  string
	Clock func() time.Time
}

func (s CourseService) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}
