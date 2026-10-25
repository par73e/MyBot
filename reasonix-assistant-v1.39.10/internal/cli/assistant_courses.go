package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reasonix/internal/assistant"
)

func assistantCourseCommand(home string, args []string) (any, error) {
	request := assistant.CourseRequest{Action: "query"}
	if args[0] == "courses-import" {
		if len(args) != 2 {
			return nil, errors.New("courses-import requires a timetable JSON file")
		}
		raw, err := os.ReadFile(args[1])
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if request.Action != "import" {
			return nil, errors.New("file must contain an import request")
		}
	} else if len(args) > 1 {
		if err := json.Unmarshal([]byte(args[1]), &request); err != nil {
			return nil, err
		}
	}
	return (assistant.CourseService{Home: home}).Apply(context.Background(), request)
}
