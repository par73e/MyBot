package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"os"
)

type CourseTool struct{ Service CourseService }

func (CourseTool) Name() string   { return "assistant_courses" }
func (CourseTool) ReadOnly() bool { return false }
func (CourseTool) Description() string {
	return "Read or update the authoritative timetable. status reads the current revision and term; query returns actual courses for an Asia/Shanghai civil date, resolving teaching weeks and exceptions. No timetable is unknown, not no courses. import replaces the timetable after checking the current revision (0 for first import); set_exception changes/cancels/moves one original occurrence, remove_exception restores it. Only current owner requests authorize changes; background runs may only query/status. Never change notification times here: use assistant_heartbeat. Course reminders and every other automatic event obey the top-level routine. Do not turn courses into personal todos or memories. Confirm mutations only after success."
}
func (CourseTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false,"required":["action"],"properties":{"action":{"type":"string","enum":["status","query","import","set_exception","remove_exception"]},"date":{"type":"string"},"revision":{"type":"integer","minimum":0},"course_id":{"type":"string"},"config":{"type":"object","description":"Full timetable: name, first_week_monday, total_weeks, timezone, periods [{number,start,end}], courses [{id,title,weekday (Monday=1),periods,weeks,room,teacher}], exceptions."},"exception":{"type":"object","additionalProperties":false,"required":["course_id","date"],"properties":{"course_id":{"type":"string"},"date":{"type":"string"},"cancelled":{"type":"boolean"},"move_to":{"type":"string"},"start":{"type":"string"},"end":{"type":"string"},"room":{"type":"string"}}}}}`)
}
func (t CourseTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var r CourseRequest
	if err := decode(args, &r); err != nil {
		return "", err
	}
	if (r.Action == "status" || r.Action == "query") && (r.Config != nil || r.Exception != nil || r.Revision != 0 || r.CourseID != "") {
		return "", errors.New("query/status cannot include mutation fields")
	}
	out, err := t.Service.Apply(ctx, r)
	if err != nil {
		return "", err
	}
	if r.Action == "query" && os.Getenv("REASONIX_ASSISTANT_COURSE_BACKGROUND") == "1" {
		if day, ok := out.(CourseDay); ok {
			if err = t.Service.recordRead(ctx, day); err != nil {
				return "", err
			}
		}
	}
	raw, err := json.Marshal(out)
	return string(raw), err
}
