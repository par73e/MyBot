package assistant

import "time"

type MorningSnapshot struct {
	WakeCourses       []CourseInstance `json:"wake_courses"`
	ID                string           `json:"id"`
	Date              string           `json:"date"`
	CreatedAt         time.Time        `json:"created_at"`
	MailIDs           []string         `json:"mail_ids"`
	MailStatus        string           `json:"mail_status"`
	OvernightMailSeen bool             `json:"overnight_mail_seen"`
	RemainingMail     int              `json:"remaining_mail"`
	Mail              []MailMessage    `json:"mail"`
	Courses           CourseDay        `json:"courses"`
	CourseStatus      string           `json:"course_status"`
	Todos             []Todo           `json:"todos"`
	News              NewsPage         `json:"news"`
	WeatherConfigured bool             `json:"weather_configured"`
}
type MorningOutput struct {
	Weather       string         `json:"weather"`
	MailDecisions []MailDecision `json:"mail_decisions"`
	News          []MorningNews  `json:"news"`
}
type MorningNews struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}
type MorningService struct {
	Home  string
	Mail  *MailService
	News  *HotNewsService
	Clock func() time.Time
}

func (s MorningService) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}
