package heartbeat

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "time/tzdata" // Servers need IANA zones even without a system zone database.
)

// Target identifies exactly one explicitly selected notification destination.
type Target struct {
	ConnectionID string `json:"connection_id"`
	Domain       string `json:"domain,omitempty"`
	ChatID       string `json:"chat_id"`
	ChatType     string `json:"chat_type,omitempty"`
}

// Run records execution separately from delivery; pending delivery never reruns AI.
type Run struct {
	StartedAt int64  `json:"startedAt"`
	EndedAt   int64  `json:"endedAt,omitempty"`
	Status    string `json:"status"`
	Result    string `json:"result,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Task is a persisted headless job. Calendar rules use the desktop interval syntax.
type Task struct {
	ID                     string  `json:"id"`
	Title                  string  `json:"title"`
	Prompt                 string  `json:"prompt"`
	Interval               string  `json:"interval,omitempty"`
	At                     string  `json:"at,omitempty"`
	Timezone               string  `json:"timezone"`
	EndsAt                 string  `json:"endsAt,omitempty"`
	Enabled                bool    `json:"enabled"`
	WorkspaceRoot          string  `json:"workspaceRoot"`
	Model                  string  `json:"model,omitempty"`
	ApprovalMode           string  `json:"approvalMode"`
	Kind                   string  `json:"kind"`
	NewConversationEachRun bool    `json:"newConversationEachRun"`
	TimeWindowStart        string  `json:"timeWindowStart,omitempty"`
	TimeWindowEnd          string  `json:"timeWindowEnd,omitempty"`
	CreatedAt              int64   `json:"createdAt"`
	LastRunAt              int64   `json:"lastRunAt,omitempty"`
	Target                 *Target `json:"target,omitempty"`
	History                []Run   `json:"history,omitempty"`
}

// Validate rejects ambiguous schedules and unsupported unattended permissions.
func (t Task) Validate() error {
	if t.ID == "" || strings.TrimSpace(t.Title) == "" || strings.TrimSpace(t.Prompt) == "" {
		return errors.New("id, title and prompt are required")
	}
	if (t.At == "") == (t.Interval == "") {
		return errors.New("set exactly one of at or interval")
	}
	if _, err := time.LoadLocation(t.Timezone); err != nil {
		return fmt.Errorf("timezone: %w", err)
	}
	if t.Kind != "agent" && t.Kind != "message" {
		return errors.New("kind must be agent or message")
	}
	if t.ApprovalMode != "read-only" && t.ApprovalMode != "workspace-write" {
		return errors.New("approvalMode must be read-only or workspace-write")
	}
	if !filepath.IsAbs(t.WorkspaceRoot) {
		return errors.New("workspaceRoot must be an absolute path")
	}
	return t.validateSchedule()
}

func (t Task) validateWorkspace() error {
	if info, err := os.Stat(t.WorkspaceRoot); err != nil || !info.IsDir() {
		return errors.New("workspaceRoot must be an existing directory")
	}
	return nil
}

func (t Task) validateSchedule() error {
	for _, value := range []string{t.At, t.EndsAt} {
		if value != "" {
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				return fmt.Errorf("time must be RFC3339 with offset: %w", err)
			}
		}
	}
	if t.Interval != "" && !isCronExpr(t.Interval) {
		if _, ok := parseHeartbeatSchedule(t.Interval); !ok {
			d, err := parseInterval(t.Interval)
			if err != nil || d <= 0 || strings.Contains(t.Interval, "|") {
				return errors.New("invalid interval/calendar rule")
			}
		}
	}
	for _, value := range []string{t.TimeWindowStart, t.TimeWindowEnd} {
		if value != "" {
			if _, _, ok := parseHeartbeatClock(value); !ok {
				return errors.New("time window must use HH:MM")
			}
		}
	}
	if t.Target != nil && (t.Target.ConnectionID == "" || t.Target.ChatID == "") {
		return errors.New("notification target requires connection_id and chat_id")
	}
	return nil
}

// Due checks expiry on every dispatch; expired jobs do not need a cleanup tick.
func (t Task) Due(now time.Time) bool {
	if !t.Enabled || t.Expired(now) {
		return false
	}
	if t.At != "" {
		at, err := time.Parse(time.RFC3339, t.At)
		return err == nil && !now.Before(at) && t.LastRunAt == 0
	}
	loc, err := time.LoadLocation(t.Timezone)
	return err == nil && scheduleDue(t, now.In(loc))
}

// Expired reports whether the exclusive end boundary has been reached.
func (t Task) Expired(now time.Time) bool {
	end, err := time.Parse(time.RFC3339, t.EndsAt)
	return err == nil && !now.Before(end)
}
