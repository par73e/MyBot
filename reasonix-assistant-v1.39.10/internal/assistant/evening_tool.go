package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strconv"
)

func eveningBinding() (string, int64, error) {
	j := os.Getenv("REASONIX_ASSISTANT_JOB_ID")
	a, e := strconv.ParseInt(os.Getenv("REASONIX_ASSISTANT_EVENING_ATTEMPT"), 10, 64)
	if os.Getenv("REASONIX_ASSISTANT_EVENING_BACKGROUND") != "1" || j == "" || e != nil {
		return "", 0, errors.New("evening snapshot requires host execution identity")
	}
	return j, a, nil
}

type EveningTool struct{ Service EveningService }

func (EveningTool) Name() string   { return "assistant_evening" }
func (EveningTool) ReadOnly() bool { return true }
func (EveningTool) Description() string {
	return "Read the host-bound 23:00 evening snapshot for this run, or preview facts / read the latest delivered evening report in the foreground. No scheduling, extra notifications or mutations. Only care phrasing is model-generated; task and class facts are host-rendered."
}
func (EveningTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false,"required":["action"],"properties":{"action":{"type":"string","enum":["snapshot","preview","latest"]}}}`)
}
func (t EveningTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var r struct {
		Action string `json:"action"`
	}
	if e := decode(args, &r); e != nil {
		return "", e
	}
	var v any
	var e error
	switch r.Action {
	case "snapshot":
		job, attempt, e := eveningBinding()
		if e != nil {
			return "", e
		}
		e = NewRepository(t.Service.Home).transact(ctx, func(tx *sql.Tx) error {
			var raw string
			if e := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='evening_snapshot' AND id=?", courseReadID(job, attempt)).Scan(&raw); e != nil {
				return e
			}
			v = json.RawMessage(raw)
			return saveEntity(ctx, tx, "evening_read", courseReadID(job, attempt), true)
		})
		if e != nil {
			return "", e
		}
	case "preview":
		v, e = t.Service.facts(ctx, "preview")
	case "latest":
		e = NewRepository(t.Service.Home).transact(ctx, func(tx *sql.Tx) error {
			var raw string
			if e := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='evening_delivered' ORDER BY rowid DESC LIMIT 1").Scan(&raw); e != nil {
				return e
			}
			v = json.RawMessage(raw)
			return nil
		})
	default:
		return "", errors.New("unknown evening action")
	}
	if e != nil {
		return "", e
	}
	b, e := json.Marshal(v)
	return string(b), e
}
