package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type AcademicTerm struct {
	Name  string `json:"name"`
	Start string `json:"start"`
	End   string `json:"end"`
}
type AcademicClosure struct {
	Start  string `json:"start"`
	End    string `json:"end"`
	Reason string `json:"reason"`
	Source string `json:"source"`
}
type AcademicAdjustment struct {
	Teaching  bool   `json:"teaching"`
	ClassDate string `json:"class_date,omitempty"`
	Reason    string `json:"reason"`
	Source    string `json:"source"`
}
type AcademicCalendar struct {
	Version      int                           `json:"version"`
	Revision     string                        `json:"revision"`
	School       string                        `json:"school"`
	ValidFrom    string                        `json:"valid_from"`
	ValidTo      string                        `json:"valid_to"`
	Terms        []AcademicTerm                `json:"terms"`
	Closures     []AcademicClosure             `json:"closures"`
	Adjustments  map[string]AcademicAdjustment `json:"adjustments"`
	HolidayYears map[string]bool               `json:"holiday_years"`
	Sources      []map[string]string           `json:"sources"`
	UpdatedAt    string                        `json:"updated_at"`
}
type AcademicDay struct {
	Date        string `json:"date"`
	Known       bool   `json:"known"`
	Teaching    bool   `json:"teaching"`
	Term        string `json:"term,omitempty"`
	ClassDate   string `json:"class_date,omitempty"`
	Reason      string `json:"reason"`
	Source      string `json:"source,omitempty"`
	Provisional bool   `json:"provisional"`
}

func loadAcademic(home string) (*AcademicCalendar, error) {
	raw, e := os.ReadFile(filepath.Join(home, "assistant", "academic-calendar.json"))
	if e != nil {
		return nil, e
	}
	if len(raw) > 512000 {
		return nil, errors.New("academic calendar too large")
	}
	var c AcademicCalendar
	if e = json.Unmarshal(raw, &c); e != nil {
		return nil, e
	}
	if e = c.validate(); e != nil {
		return nil, e
	}
	return &c, nil
}
func (c AcademicCalendar) validate() error {
	valid := func(v string) bool { _, e := courseDate(v); return e == nil }
	if c.Version != 1 || c.Revision == "" || !valid(c.ValidFrom) || !valid(c.ValidTo) || c.ValidFrom > c.ValidTo || len(c.Terms) == 0 || len(c.Terms) > 8 || len(c.Adjustments) > 1000 || len(c.Closures) > 200 {
		return errors.New("invalid academic calendar")
	}
	for _, t := range c.Terms {
		if !valid(t.Start) || !valid(t.End) || t.Start > t.End || t.Start < c.ValidFrom || t.End > c.ValidTo {
			return errors.New("invalid academic term")
		}
	}
	for _, r := range c.Closures {
		if !valid(r.Start) || !valid(r.End) || r.Start > r.End {
			return errors.New("invalid academic closure")
		}
	}
	for d, a := range c.Adjustments {
		if !valid(d) || a.ClassDate != "" && !valid(a.ClassDate) {
			return errors.New("invalid academic date adjustment")
		}
	}
	return nil
}
func (c AcademicCalendar) Day(at time.Time) AcademicDay {
	at = at.In(mustShanghai())
	date := at.Format("2006-01-02")
	out := AcademicDay{Date: date, ClassDate: date, Reason: "超出已导入校历范围"}
	if date < c.ValidFrom || date > c.ValidTo {
		return out
	}
	out.Known = true
	out.Provisional = !c.HolidayYears[at.Format("2006")]
	inTerm := false
	for _, t := range c.Terms {
		if date >= t.Start && date <= t.End {
			out.Term = t.Name
			inTerm = true
			break
		}
	}
	if !inTerm {
		out.Reason = "寒暑假或非教学学期"
		return out
	}
	if a, ok := c.Adjustments[date]; ok {
		out.Teaching = a.Teaching
		out.Reason = a.Reason
		out.Source = a.Source
		if a.ClassDate != "" {
			out.ClassDate = a.ClassDate
		}
		return out
	}
	for _, r := range c.Closures {
		if date >= r.Start && date <= r.End {
			out.Reason = r.Reason
			out.Source = r.Source
			return out
		}
	}
	out.Teaching = at.Weekday() != time.Saturday && at.Weekday() != time.Sunday
	if out.Teaching {
		out.Reason = "学期内正常教学日"
	} else {
		out.Reason = "学期内周末休息日"
	}
	return out
}

// Teaching rules determine residence spans, rather than equating an empty
// timetable with returning home. Confirmed personal plans are resolved first.
func (c AcademicCalendar) Location(at time.Time, school, home string) (id, basis string, known bool) {
	day := at.In(mustShanghai())
	date := day.Format("2006-01-02")
	if date < c.ValidFrom || date > c.ValidTo {
		return "", "超出已导入校历范围", false
	}
	midnight, _ := courseDate(date)
	today := c.Day(day)
	yesterday := c.Day(midnight.AddDate(0, 0, -1))
	tomorrow := c.Day(midnight.AddDate(0, 0, 1))
	minute := day.Hour()*60 + day.Minute()
	// A single free day between teaching days does not imply a round trip.
	if today.Teaching {
		if tomorrow.Teaching || minute < 19*60 {
			return school, today.Reason, true
		}
		return home, "教学安排结束，按19:00返家分界推断", true
	}
	if yesterday.Teaching && tomorrow.Teaching {
		return school, "两个教学日之间的单日休息，默认继续在校", true
	}
	if tomorrow.Teaching && minute >= 17*60 {
		return school, "明天有教学安排，按前一天17:00返校分界推断", true
	}
	return home, today.Reason + "，按通常回家安排推断", true
}

type AcademicTool struct{ Home string }

func (AcademicTool) Name() string   { return "assistant_academic" }
func (AcademicTool) ReadOnly() bool { return true }
func (AcademicTool) Description() string {
	return "Read the owner's imported official school calendar and national holiday/makeup-day rules. query one civil date or status for the next 14 days. Teaching-day status is separate from actual residence: location plans override the calendar's home/school inference. class_date denotes which original date's classes a makeup day follows, preserving teaching week. Missing/unconfirmed annual holiday notices stay provisional; never invent future makeup days. The configured native schoolday_reminder sends a fixed UU remote-PC reminder without invoking a model. This read never schedules, imports, sends, or changes personal data."
}
func (AcademicTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false,"required":["action"],"properties":{"action":{"type":"string","enum":["status","query"]},"date":{"type":"string"}}}`)
}
func (t AcademicTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var r struct {
		Action string `json:"action"`
		Date   string `json:"date"`
	}
	if e := decode(args, &r); e != nil {
		return "", e
	}
	if r.Action != "query" && r.Action != "status" {
		return "", errors.New("unknown academic operation")
	}
	c, e := loadAcademic(t.Home)
	if os.IsNotExist(e) {
		return `{"configured":false}`, nil
	}
	if e != nil {
		return "", errors.New("校历暂时无法读取，不能据此判断是否上学")
	}
	var result any
	now := time.Now().In(mustShanghai())
	if r.Date != "" {
		now, e = courseDate(r.Date)
		if e != nil {
			return "", e
		}
	}
	if r.Action == "query" {
		result = c.Day(now)
	} else if r.Action == "status" {
		days := []AcademicDay{}
		for i := 0; i < 14; i++ {
			days = append(days, c.Day(now.AddDate(0, 0, i)))
		}
		result = map[string]any{"configured": true, "calendar": c, "days": days, "today": c.Day(now), "sync": academicSyncState(t.Home)}
	} else {
		return "", errors.New("unknown academic operation")
	}
	b, e := json.Marshal(result)
	return string(b), e
}
func academicSyncState(home string) any {
	var state map[string]any
	raw, e := os.ReadFile(filepath.Join(home, "assistant", "academic-sync-status.json"))
	if e != nil || len(raw) > 64000 {
		return nil
	}
	if json.Unmarshal(raw, &state) != nil {
		return nil
	}
	return state
}
