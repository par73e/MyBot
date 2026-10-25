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
	ModuleID         string `json:"module_id,omitempty"`
	TriggerRevision  int    `json:"trigger_revision,omitempty"`
	ScheduledAt      string `json:"scheduled_at,omitempty"`
	ConversationID   string `json:"conversationId,omitempty"`
	DeliveryAttempts int    `json:"deliveryAttempts,omitempty"`
	NextDeliveryAt   int64  `json:"nextDeliveryAt,omitempty"`
	DecisionReason   string `json:"decisionReason,omitempty"`
	StartedAt        int64  `json:"startedAt"`
	EndedAt          int64  `json:"endedAt,omitempty"`
	Status           string `json:"status"`
	Result           string `json:"result,omitempty"`
	SessionID        string `json:"sessionId,omitempty"`
	Error            string `json:"error,omitempty"`
}

// Task is a persisted headless job. Calendar rules use the desktop interval syntax.
type Task struct {
	// Capability and MailIDs are host-created execution scope, never mail/model arguments.
	CourseID        string   `json:"course_id,omitempty"`
	CourseDate      string   `json:"course_date,omitempty"`
	Capability      string   `json:"capability,omitempty"`
	PolicyID        string   `json:"policy_id,omitempty"`
	Revision        int      `json:"revision,omitempty"`
	MailIDs         []string `json:"mail_ids,omitempty"`
	ModuleID        string   `json:"module_id,omitempty"`
	TriggerID       string   `json:"trigger_id,omitempty"`
	OccurrenceID    string   `json:"occurrence_id,omitempty"`
	TriggerRevision int      `json:"trigger_revision,omitempty"`
	// RunStartedAt is host-only admission time, not persisted task configuration.
	RunStartedAt           int64          `json:"-"`
	ID                     string         `json:"id"`
	Title                  string         `json:"title"`
	Prompt                 string         `json:"prompt"`
	Interval               string         `json:"interval,omitempty"`
	DecideNotification     bool           `json:"decide_notification,omitempty"`
	SkipUntil              string         `json:"skip_until,omitempty"`
	SnoozedUntil           string         `json:"snoozed_until,omitempty"`
	At                     string         `json:"at,omitempty"`
	Timezone               string         `json:"timezone"`
	EndsAt                 string         `json:"endsAt,omitempty"`
	Enabled                bool           `json:"enabled"`
	WorkspaceRoot          string         `json:"workspaceRoot"`
	Model                  string         `json:"model,omitempty"`
	ApprovalMode           string         `json:"approvalMode"`
	Kind                   string         `json:"kind"`
	NewConversationEachRun bool           `json:"newConversationEachRun"`
	TimeWindowStart        string         `json:"timeWindowStart,omitempty"`
	TimeWindowEnd          string         `json:"timeWindowEnd,omitempty"`
	WeeklySchedule         WeeklySchedule `json:"weeklySchedule,omitempty"`
	DateOverrides          DateOverrides  `json:"dateOverrides,omitempty"`
	CreatedAt              int64          `json:"createdAt"`
	LastRunAt              int64          `json:"lastRunAt,omitempty"`
	Target                 *Target        `json:"target,omitempty"`
	History                []Run          `json:"history,omitempty"`
}

// Validate rejects ambiguous schedules and unsupported unattended permissions.
func (t Task) Validate() error {
	if t.Capability != "" && t.Capability != "mail" && t.Capability != "weather" && t.Capability != "course" && t.Capability != "morning" && t.Capability != "news" {
		return errors.New("unknown host capability")
	}
	if (t.Capability == "mail" || t.Capability == "weather" || t.Capability == "course" || t.Capability == "morning" || t.Capability == "news") && (t.Kind != "agent" || !t.DecideNotification || t.ApprovalMode != "read-only") {
		return errors.New("scoped analysis requires a read-only notification agent")
	}
	if t.ID == "" || strings.TrimSpace(t.Title) == "" || strings.TrimSpace(t.Prompt) == "" {
		return errors.New("id, title and prompt are required")
	}
	if (t.At == "") == (t.Interval == "") {
		return errors.New("set exactly one of at or interval")
	}
	if _, err := time.LoadLocation(t.Timezone); err != nil {
		return fmt.Errorf("timezone: %w", err)
	}
	if t.Kind != "agent" && t.Kind != "message" && t.Kind != "mail_check" && t.Kind != "weather_check" {
		return errors.New("kind must be agent, message or mail_check")
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
	if err := t.DateOverrides.Validate(); err != nil {
		return err
	}
	if t.WeeklySchedule != nil {
		if t.TimeWindowStart != "" || t.TimeWindowEnd != "" {
			return errors.New("weeklySchedule and daily window cannot be combined")
		}
		if err := t.WeeklySchedule.Validate(); err != nil {
			return err
		}
	}
	for _, value := range []string{t.At, t.EndsAt, t.SkipUntil, t.SnoozedUntil} {
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
	if skip, err := time.Parse(time.RFC3339, t.SkipUntil); err == nil && now.Before(skip) {
		return false
	}
	if (t.Kind == "mail_check" || t.Kind == "weather_check" || t.WeeklySchedule != nil || len(t.DateOverrides) > 0) && !t.WithinTimeWindow(now) {
		return false
	}
	if (t.Kind == "mail_check" || t.Kind == "weather_check") && t.LastRunAt == 0 {
		return true
	}
	if snooze, err := time.Parse(time.RFC3339, t.SnoozedUntil); err == nil {
		return !now.Before(snooze)
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

// WithinTimeWindow evaluates local wall-clock boundaries independently of due time.
func (t Task) WithinTimeWindow(now time.Time) bool {
	loc, err := time.LoadLocation(t.Timezone)
	if err != nil {
		return false
	}
	if allowed, matched := t.DateOverrides.allows(now.In(loc)); matched {
		return allowed
	}
	if t.WeeklySchedule != nil {
		return t.WeeklySchedule.Allows(now.In(loc))
	}
	return heartbeatWithinTimeWindow(t, now.In(loc))
}
