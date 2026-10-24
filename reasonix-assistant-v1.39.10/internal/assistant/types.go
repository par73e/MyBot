package assistant

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"reasonix/internal/heartbeat"
)

// Options binds trusted host routing; model arguments cannot select an owner,
// notification recipient, workspace, credentials path or permission preset.
type Options struct {
	TriggerID       string
	OccurrenceID    string
	TriggerRevision int
	MailScheduleID  string
	Workspace       string
	Model           string
	Target          *heartbeat.Target
}

// Todo is a personal obligation, independent from its reminder delivery status.
type Todo struct {
	DueAt string `json:"due_at,omitempty"`

	ID          string      `json:"id"`
	Revision    int         `json:"revision"`
	Title       string      `json:"title"`
	Body        string      `json:"body"`
	Status      string      `json:"status"`
	Kind        string      `json:"kind"`
	JobID       string      `json:"job_id,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	Completions []time.Time `json:"completions,omitempty"`
}

// TaskRequest is validated input shared by the CLI, tools and tests.
type TaskRequest struct {
	DueAt string `json:"due_at,omitempty"`

	Action         string `json:"action"`
	ID             string `json:"id,omitempty"`
	Revision       int    `json:"revision,omitempty"`
	Title          string `json:"title,omitempty"`
	Body           string `json:"body,omitempty"`
	Kind           string `json:"kind,omitempty"`
	At             string `json:"at,omitempty"`
	Interval       string `json:"interval,omitempty"`
	EndsAt         string `json:"ends_at,omitempty"`
	Timezone       string `json:"timezone,omitempty"`
	OccurrenceOnly bool   `json:"occurrence_only,omitempty"`
	IncludeClosed  bool   `json:"include_closed,omitempty"`
}

// TaskView joins the user-facing obligation with its independently tracked job.
type TaskView struct {
	Todo
	Schedule *heartbeat.Task `json:"schedule,omitempty"`
}

func newID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func timestamp(s string) (time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, errors.New("time must be RFC3339 including a time-zone offset")
	}
	return t, nil
}
