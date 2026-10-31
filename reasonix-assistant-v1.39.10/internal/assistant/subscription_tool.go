package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"time"
)

type SubscriptionTool struct{ Service Subscriptions }

func (SubscriptionTool) Name() string   { return "assistant_subscriptions" }
func (SubscriptionTool) ReadOnly() bool { return false }
func (SubscriptionTool) Description() string {
	return neutralInstanceInstructions("Manage expiry items: services, memberships, domains, servers, vouchers, certificates and other owned items with a real expiration date. Distinguish by meaning, not duration: an item expiring belongs here; an action Sir must complete belongs in assistant_tasks, however distant. Respect Sir's explicit preference. If both meanings are present, use the requested behavior and do not automatically duplicate records. For a new expiry use the configured default lead (initially 7 days), or Sir's explicit reminder_days or reminder_on; these replace the default, not add extra reminders. Date reminders run at the effective routine start. If Sir requests an exact clock time, this tool cannot honor it; explain and use a separately scheduled reminder only when appropriate. Tell Sir the actual saved expiry and reminder date, and warn if the reminder date has passed or is outside the routine. Do not invent an expiry or infer renewal. get_default/set_default configure the lead for future new records only. list/get before changes with revision; archive stops reminders, not vendor service. Email-derived records require Sir's confirmation. No payments, cancellations or vendor login.")
}
func (SubscriptionTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false,"required":["action"],"properties":{"action":{"type":"string","enum":["create","list","get","update","renew","archive","get_default","set_default"]},"id":{"type":"string"},"revision":{"type":"integer"},"name":{"type":"string"},"expires_on":{"type":"string"},"expires_at":{"type":"string"},"renewal_mode":{"type":"string","enum":["manual","auto","unknown"]},"reminder_days":{"type":"integer","minimum":0,"maximum":3650,"description":"Days before expiry; set_default changes the default for future records."},"reminder_on":{"type":"string","description":"Explicit reminder date YYYY-MM-DD; mutually exclusive with reminder_days."}}}`)
}
func (t SubscriptionTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var r SubscriptionRequest
	if e := decode(args, &r); e != nil {
		return "", e
	}
	v, e := t.Service.Apply(ctx, r)
	if e != nil {
		return "", e
	}
	if item, ok := v.(Subscription); ok && !item.Archived {
		expiry, err := item.expiry()
		if err == nil {
			leads := item.reminderLeads(expiry)
			if len(leads) == 1 {
				date := expiry.AddDate(0, 0, -leads[0]).Format("2006-01-02")
				warning := ""
				reminderAt := ""
				var config HeartbeatConfig
				err := t.Service.Repository.transact(ctx, func(tx *sql.Tx) error { var e error; config, e = loadHeartbeatConfig(ctx, tx); return e })
				if err != nil {
					warning = "Could not check the routine; reminder delivery time is unverified."
				} else {
					found := false
					for _, tr := range config.Triggers {
						if tr.Mode != "subscription" || !config.ModuleEnabled["subscription_reminder"] {
							continue
						}
						start, _, enabled := config.TriggerBounds(tr, date)
						if !enabled || !slices.Contains(config.Selected(tr, date), "subscription_reminder") || (item.ExpiresAt != "" && !start.Before(expiry)) {
							continue
						}
						found = true
						reminderAt = start.Format(time.RFC3339)
						break
					}
					if !found {
						warning = "No active expiry reminder window on this date; the reminder will be skipped unless the schedule changes."
					}
				}
				if date <= time.Now().In(mustShanghai()).Format("2006-01-02") {
					warning += " Reminder date is today or in the past; check whether its delivery window has already passed. It is not deferred automatically."
				}
				v = struct {
					Subscription
					ReminderDate string `json:"reminder_date"`
					ReminderAt   string `json:"reminder_at,omitempty"`
					Warning      string `json:"reminder_warning,omitempty"`
				}{item, date, reminderAt, warning}
			}
		}
	}
	b, e := json.Marshal(v)
	return string(b), e
}
