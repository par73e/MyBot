package assistant

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/heartbeat"
)

func TestExpiryReminderPolicies(t *testing.T) {
	h, _, _ := expansionFixture(t)
	svc := Subscriptions{Repository: h.repo()}
	out, err := svc.Apply(t.Context(), SubscriptionRequest{Action: "create", Name: "券", ExpiresOn: "2026-11-02", ReminderOn: "2026-10-25"})
	if err != nil {
		t.Fatal(err)
	}
	item := out.(Subscription)
	var original heartbeat.Task
	for _, j := range atHeartbeat(t, h, "2026-10-25T11:00:00+08:00") {
		if j.PolicyID == item.ID {
			original = j
		}
	}
	if original.At != "2026-10-25T11:00:00+08:00" {
		t.Fatal(original)
	}
	days := 2
	out, err = svc.Apply(t.Context(), SubscriptionRequest{Action: "update", ID: item.ID, Revision: item.Revision, ReminderDays: &days})
	if err != nil {
		t.Fatal(err)
	}
	item = out.(Subscription)
	if ok, err := h.Allows(t.Context(), original, timeMust(original.At)); ok || !errors.Is(err, heartbeat.ErrOccurrenceChanged) {
		t.Fatal(ok, err)
	}
	var replacement heartbeat.Task
	for _, j := range atHeartbeat(t, h, "2026-10-31T11:00:00+08:00") {
		if j.PolicyID == item.ID {
			replacement = j
		}
	}
	if replacement.At != "2026-10-31T11:00:00+08:00" {
		t.Fatal(replacement)
	}
	if _, err = svc.Apply(t.Context(), SubscriptionRequest{Action: "update", ID: item.ID, Revision: item.Revision, ReminderOn: "2026-11-03"}); err == nil {
		t.Fatal("accepted reminder after expiry")
	}
}

func TestExpiryDefaultAndLegacyCompatibility(t *testing.T) {
	h, _, _ := expansionFixture(t)
	svc := Subscriptions{Repository: h.repo()}
	days := 4
	if _, err := svc.Apply(t.Context(), SubscriptionRequest{Action: "set_default", ReminderDays: &days}); err != nil {
		t.Fatal(err)
	}
	out, err := svc.Apply(t.Context(), SubscriptionRequest{Action: "create", Name: "证件", ExpiresOn: "2026-11-02"})
	if err != nil {
		t.Fatal(err)
	}
	item := out.(Subscription)
	if item.ReminderDays == nil || *item.ReminderDays != 4 {
		t.Fatal(item)
	}
	days = 2
	if _, err = svc.Apply(t.Context(), SubscriptionRequest{Action: "set_default", ReminderDays: &days}); err != nil {
		t.Fatal(err)
	}
	out, err = svc.Apply(t.Context(), SubscriptionRequest{Action: "get", ID: item.ID})
	if err != nil || *out.(Subscription).ReminderDays != 4 {
		t.Fatal(out, err)
	}
	legacy := Subscription{ID: "legacy", Revision: 1, Period: 1, Name: "旧会员", ExpiresOn: "2026-11-01", RenewalMode: "unknown"}
	if err = h.repo().transact(t.Context(), func(tx *sql.Tx) error { return saveEntity(context.Background(), tx, "subscription", legacy.ID, legacy) }); err != nil {
		t.Fatal(err)
	}
	expiry, _ := legacy.expiry()
	leads := legacy.reminderLeads(expiry)
	if len(leads) != 3 || leads[0] != 7 || leads[1] != 1 || leads[2] != 0 {
		t.Fatal(leads)
	}
	var found bool
	for _, j := range atHeartbeat(t, h, "2026-10-25T11:00:00+08:00") {
		if j.PolicyID == legacy.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("legacy schedule lost")
	}
}

func TestExpiryFixedDateNeedsReplanningOnRenewal(t *testing.T) {
	h, _, _ := expansionFixture(t)
	svc := Subscriptions{Repository: h.repo()}
	out, err := svc.Apply(t.Context(), SubscriptionRequest{Action: "create", Name: "域名", ExpiresOn: "2026-11-01", ReminderOn: "2026-10-25"})
	if err != nil {
		t.Fatal(err)
	}
	item := out.(Subscription)
	_, err = svc.Apply(t.Context(), SubscriptionRequest{Action: "renew", ID: item.ID, Revision: item.Revision, ExpiresOn: "2027-11-01"})
	if err == nil {
		t.Fatal("renewal must not silently reuse a past fixed reminder date")
	}
	_, err = svc.Apply(t.Context(), SubscriptionRequest{Action: "create", Name: "域名", ExpiresOn: "2026-11-01", ReminderOn: "2026-10-26"})
	if err == nil {
		t.Fatal("duplicate with different reminder must not silently reuse old policy")
	}
}

func TestExpiryToolReportsReminderAndWarnings(t *testing.T) {
	h, _, _ := expansionFixture(t)
	tool := SubscriptionTool{Service: Subscriptions{Repository: h.repo()}}
	result, err := tool.Execute(t.Context(), []byte(`{"action":"create","name":"测试券","expires_on":"2026-11-02","reminder_on":"2026-10-25"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, `"reminder_date":"2026-10-25"`) || !strings.Contains(result, `"reminder_at":"2026-10-25T11:00:00+08:00"`) {
		t.Fatal(result)
	}
}

func TestExpiryVoucherPlanAtProductionRoutine(t *testing.T) {
	h, _, _ := expansionFixture(t)
	err := h.repo().transact(t.Context(), func(tx *sql.Tx) error {
		c, err := loadHeartbeatConfig(t.Context(), tx)
		if err != nil {
			return err
		}
		sunday := c.Weekly["sunday"]
		sunday.Start = "10:58"
		c.Weekly["sunday"] = sunday
		return saveEntity(t.Context(), tx, "heartbeat_config", heartbeatConfigID, c)
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := Subscriptions{Repository: h.repo()}
	out, err := svc.Apply(t.Context(), SubscriptionRequest{Action: "create", Name: "示例到期事项", ExpiresOn: "2027-01-18", ReminderOn: "2027-01-10"})
	if err != nil {
		t.Fatal(err)
	}
	item := out.(Subscription)
	var found heartbeat.Task
	for _, j := range atHeartbeat(t, h, "2027-01-10T10:58:00+08:00") {
		if j.PolicyID == item.ID {
			found = j
		}
	}
	if found.At != "2027-01-10T10:58:00+08:00" {
		t.Fatal(found)
	}
	if ok, err := h.Allows(t.Context(), found, timeMust(found.At)); !ok || err != nil {
		t.Fatal(ok, err)
	}
}
