package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"reasonix/internal/heartbeat"
)

type Subscription struct {
	ID          string `json:"id"`
	Revision    int    `json:"revision"`
	Period      int    `json:"period"`
	Name        string `json:"name"`
	ExpiresOn   string `json:"expires_on,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	RenewalMode string `json:"renewal_mode"`
	Archived    bool   `json:"archived,omitempty"`
}
type SubscriptionRequest struct {
	Action      string `json:"action"`
	ID          string `json:"id,omitempty"`
	Revision    int    `json:"revision,omitempty"`
	Name        string `json:"name,omitempty"`
	ExpiresOn   string `json:"expires_on,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	RenewalMode string `json:"renewal_mode,omitempty"`
}
type Subscriptions struct{ Repository Repository }

func (v Subscription) expiry() (time.Time, error) {
	if (v.ExpiresOn == "") == (v.ExpiresAt == "") {
		return time.Time{}, errors.New("provide exactly one expires_on date or expires_at timestamp; do not guess expiry")
	}
	if v.ExpiresOn != "" {
		return courseDate(v.ExpiresOn)
	}
	t, e := timestamp(v.ExpiresAt)
	return t.In(mustShanghai()), e
}
func (s Subscriptions) Apply(ctx context.Context, r SubscriptionRequest) (any, error) {
	if r.ExpiresOn != "" && r.ExpiresAt != "" {
		return nil, errors.New("expires_on and expires_at are mutually exclusive")
	}
	if err := allowMutation(r.Action); err != nil {
		return nil, err
	}
	var result any
	err := s.Repository.transact(ctx, func(tx *sql.Tx) error {
		all, e := loadEntities[Subscription](ctx, tx, "subscription")
		if e != nil {
			return e
		}
		if r.Action == "list" {
			active := []Subscription{}
			for _, v := range all {
				if !v.Archived {
					active = append(active, v)
				}
			}
			result = active
			return nil
		}
		v := Subscription{Revision: 1, Period: 1, RenewalMode: "unknown"}
		if r.Action == "create" {
			if len(all) >= 500 {
				return errors.New("subscription capacity reached")
			}
			v.ID, e = newID()
			if e != nil {
				return e
			}
		} else {
			v, e = existingSubscription(all, r)
			if e != nil {
				return e
			}
			if r.Action == "get" {
				result = v
				return nil
			}
		}
		if e = applySubscriptionFields(&v, r); e != nil {
			return e
		}
		if r.Action == "create" {
			for _, old := range all {
				if !old.Archived && old.Name == v.Name && old.ExpiresOn == v.ExpiresOn && old.ExpiresAt == v.ExpiresAt {
					result = old
					return nil
				}
			}
		}
		if e = saveEntity(ctx, tx, "subscription", v.ID, v); e != nil {
			return e
		}
		jobs, e := loadEntities[heartbeat.Task](ctx, tx, "job")
		if e != nil {
			return e
		}
		for _, j := range jobs {
			if j.ModuleID == "subscription_reminder" && j.PolicyID == v.ID && j.Enabled {
				j.Enabled = false
				if e = saveEntity(ctx, tx, "job", j.ID, j); e != nil {
					return e
				}
			}
		}
		result = v
		return nil
	})
	return result, err
}

func subscriptionJob(s HeartbeatService, c HeartbeatConfig, tr ModuleTrigger, v Subscription, day time.Time, lead int, now time.Time) (heartbeat.Task, bool) {
	start, end, ok := c.TriggerBounds(tr, day.Format("2006-01-02"))
	if !ok || !slices.Contains(c.Selected(tr, day.Format("2006-01-02")), "subscription_reminder") {
		return heartbeat.Task{}, false
	}
	expires, e := v.expiry()
	if e != nil {
		return heartbeat.Task{}, false
	}
	if v.ExpiresAt != "" && !start.Before(expires) {
		return heartbeat.Task{}, false
	}
	label := v.ExpiresOn
	if label == "" {
		label = expires.Format("2006-01-02 15:04")
	}
	action := "到期"
	if v.RenewalMode == "auto" {
		action = "到期（已记录为自动续费，请留意续费安排）"
	}
	text := fmt.Sprintf("Sir，%s将于%s%s，记得留意。", v.Name, label, action)
	deadline := start.Add(10 * time.Minute)
	if end.Before(deadline) {
		deadline = end
	}
	id := fmt.Sprintf("subscription-%s-%d-%d", v.ID, v.Period, lead)
	j := heartbeat.Task{ID: id, Title: "到期提醒 / " + v.Name, Prompt: text, At: start.Format(time.RFC3339), EndsAt: deadline.Format(time.RFC3339), Timezone: "Asia/Shanghai", Enabled: true, WorkspaceRoot: s.Options.Workspace, ApprovalMode: "read-only", Kind: "message", Target: s.Options.Target, CreatedAt: now.UnixMilli(), ModuleID: "subscription_reminder", TriggerID: tr.ID, PolicyID: v.ID, Revision: v.Revision}
	return j, true
}
func (s HeartbeatService) prepareSubscriptions(ctx context.Context, tx *sql.Tx, c HeartbeatConfig, tr ModuleTrigger, now time.Time) error {
	if !tr.Enabled || !c.ModuleEnabled["subscription_reminder"] {
		return nil
	}
	all, e := loadEntities[Subscription](ctx, tx, "subscription")
	if e != nil {
		return e
	}
	for _, v := range all {
		if v.Archived {
			continue
		}
		expiry, e := v.expiry()
		if e != nil {
			return e
		}
		for _, lead := range []int{7, 1, 0} {
			day := expiry.AddDate(0, 0, -lead)
			if day.Format("2006-01-02") != now.Format("2006-01-02") {
				continue
			}
			j, ok := subscriptionJob(s, c, tr, v, day, lead, now)
			if !ok {
				continue
			}
			var raw string
			e = tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='job' AND id=?", j.ID).Scan(&raw)
			if e == nil {
				var old heartbeat.Task
				if e = json.Unmarshal([]byte(raw), &old); e != nil {
					return e
				}
				if len(old.History) > 0 {
					continue
				}
			} else if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if now.After(timeMust(j.EndsAt)) {
				continue
			}
			if e = j.Validate(); e != nil {
				return e
			}
			if e = saveEntity(ctx, tx, "job", j.ID, j); e != nil {
				return e
			}
		}
	}
	return nil
}
func timeMust(s string) time.Time { v, _ := time.Parse(time.RFC3339, s); return v }
func (s HeartbeatService) subscriptionAdmission(ctx context.Context, c HeartbeatConfig, t heartbeat.Task, now time.Time) (bool, error) {
	tr, ok := c.Triggers[t.TriggerID]
	if !ok || !tr.Enabled || !c.ModuleEnabled[t.ModuleID] || t.Expired(now) {
		return false, heartbeat.ErrOccurrenceChanged
	}
	valid := false
	e := s.repo().transact(ctx, func(tx *sql.Tx) error {
		all, e := loadEntities[Subscription](ctx, tx, "subscription")
		if e != nil {
			return e
		}
		for _, v := range all {
			if v.ID != t.PolicyID || v.Archived || v.Revision != t.Revision {
				continue
			}
			expiry, e := v.expiry()
			if e != nil {
				return e
			}
			for _, lead := range []int{7, 1, 0} {
				j, ok := subscriptionJob(s, c, tr, v, expiry.AddDate(0, 0, -lead), lead, now)
				if ok && j.ID == t.ID && j.At == t.At && j.Prompt == t.Prompt {
					valid = true
				}
			}
		}
		return nil
	})
	if e == nil && !valid {
		return false, heartbeat.ErrOccurrenceChanged
	}
	return valid, e
}

func existingSubscription(all []Subscription, r SubscriptionRequest) (Subscription, error) {
	var v Subscription

	found := false
	for _, old := range all {
		if old.ID == r.ID {
			v = old
			found = true
			break
		}
	}
	if !found {
		return v, errors.New("subscription not found")
	}
	if r.Action == "get" {
		return v, nil
	}
	if v.Revision != r.Revision {
		return v, errors.New("subscription revision conflict; reload")
	}
	if r.Action != "update" && r.Action != "renew" && r.Action != "archive" {
		return v, errors.New("unknown subscription action")
	}
	if v.Archived {
		return v, errors.New("subscription archived; create a new record")
	}
	if r.Action == "renew" && r.ExpiresOn == "" && r.ExpiresAt == "" {
		return v, errors.New("renew requires the actual new expiry date")
	}
	if (r.ExpiresOn != "" && r.ExpiresOn != v.ExpiresOn) || (r.ExpiresAt != "" && r.ExpiresAt != v.ExpiresAt) {
		v.Period++
	}
	v.Revision++
	return v, nil
}

func applySubscriptionFields(v *Subscription, r SubscriptionRequest) error {
	if r.Action == "archive" {
		v.Archived = true
	}
	if r.Name != "" {
		v.Name = strings.TrimSpace(r.Name)
	}
	if r.ExpiresOn != "" {
		v.ExpiresOn = r.ExpiresOn
		v.ExpiresAt = ""
	}
	if r.ExpiresAt != "" {
		v.ExpiresAt = r.ExpiresAt
		v.ExpiresOn = ""
	}
	if r.RenewalMode != "" {
		v.RenewalMode = r.RenewalMode
	}
	if v.Name == "" || len(v.Name) > 512 || strings.ContainsAny(v.Name, "\r\n") {
		return errors.New("subscription requires a short name")
	}
	if _, e := v.expiry(); e != nil {
		return e
	}
	if !slices.Contains([]string{"unknown", "manual", "auto"}, v.RenewalMode) {
		return errors.New("renewal_mode must be unknown, manual or auto")
	}
	return nil
}
