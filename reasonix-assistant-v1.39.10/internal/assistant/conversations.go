package assistant

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const DailyTimezone = "Asia/Shanghai"

// Conversation dates are assistant days [06:00, next 06:00), not creation dates.
type Conversation struct {
	Owner     string    `json:"-"`
	Date      string    `json:"date"`
	SessionID string    `json:"session_id"`
	Title     string    `json:"title"`
	Legacy    bool      `json:"legacy,omitempty"`
	StartsAt  time.Time `json:"starts_at"`
	EndsAt    time.Time `json:"ends_at"`
}

// ConversationOwner is host-selected; it is never a model tool argument.
func ConversationOwner(connection, chat, workspace string) string {
	stable := ""
	if os.Getenv("ASROBOT_INSTANCE_MODE") == "isolated" && workspace != "" && filepath.Clean(workspace) == os.Getenv("ASROBOT_RUNTIME_WORKSPACE") {
		stable = os.Getenv("REASONIX_CONVERSATION_WORKSPACE_ID")
	}
	if filepath.IsAbs(stable) && filepath.Clean(stable) == stable {
		// This host-selected identifier represents the logical workspace, not a
		// path to access. Migration may preserve a former canonical workspace ID.
		workspace = stable
	} else if workspace != "" {
		workspace = filepath.Clean(workspace)
		if resolved, err := filepath.EvalSymlinks(workspace); err == nil {
			workspace = resolved
		}
	}
	h := sha256.Sum256([]byte(connection + "\x00" + chat + "\x00" + workspace))
	return fmt.Sprintf("%x", h[:16])
}

func DailyConversation(owner string, now time.Time) Conversation {
	loc, _ := time.LoadLocation(DailyTimezone) // tzdata is embedded by heartbeat.
	day := now.In(loc).Add(-6 * time.Hour)
	start := time.Date(day.Year(), day.Month(), day.Day(), 6, 0, 0, 0, loc)
	date := start.Format(time.DateOnly)
	return Conversation{Owner: owner, Date: date, SessionID: "friday-" + owner + "-" + start.Format("20060102"), Title: date, StartsAt: start, EndsAt: start.AddDate(0, 0, 1)}
}

func dailyTables(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS assistant_daily_sessions(owner TEXT NOT NULL,date TEXT NOT NULL,session_id TEXT NOT NULL UNIQUE,payload TEXT NOT NULL,PRIMARY KEY(owner,date));
	CREATE TABLE IF NOT EXISTS assistant_notifications(owner TEXT NOT NULL,id TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(owner,id));`)
	return err
}

func (r Repository) EnsureConversation(ctx context.Context, c Conversation) error {
	return r.transact(ctx, func(tx *sql.Tx) error {
		if err := dailyTables(ctx, tx); err != nil {
			return err
		}
		raw, err := json.Marshal(c)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO assistant_daily_sessions(owner,date,session_id,payload) VALUES(?,?,?,?) ON CONFLICT(owner,date) DO NOTHING", c.Owner, c.Date, c.SessionID, raw)
		if err != nil {
			return err
		}
		var id string
		if err = tx.QueryRowContext(ctx, "SELECT session_id FROM assistant_daily_sessions WHERE owner=? AND date=?", c.Owner, c.Date).Scan(&id); err != nil {
			return err
		}
		if id != c.SessionID {
			return errors.New("daily conversation identity conflict")
		}
		return nil
	})
}

// Conversations uses a bounded keyset query. A date range refers to day labels.
func (r Repository) Conversations(ctx context.Context, owner, from, to, before string, limit int) ([]Conversation, error) {
	out := []Conversation{}
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, 100)
	err := r.transact(ctx, func(tx *sql.Tx) error {
		if err := dailyTables(ctx, tx); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT payload FROM assistant_daily_sessions WHERE owner=? AND (?='' OR date>=?) AND (?='' OR date<=?) AND (?='' OR date<?) ORDER BY date DESC LIMIT ?`, owner, from, from, to, to, before, before, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			var c Conversation
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(raw), &c); err != nil {
				return err
			}
			c.Owner = owner
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

func (r Repository) Conversation(ctx context.Context, owner, id string) (Conversation, error) {
	var c Conversation
	err := r.transact(ctx, func(tx *sql.Tx) error {
		if err := dailyTables(ctx, tx); err != nil {
			return err
		}
		var raw string
		if err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_daily_sessions WHERE owner=? AND session_id=?", owner, id).Scan(&raw); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return err
		}
		c.Owner = owner
		return nil
	})
	return c, err
}

// LegacyConversationID locates the previous deterministic Weixin DM without
// renaming or rewriting that undivided transcript.
func LegacyConversationID(connection, chat string) string {
	digest := sha256.Sum256([]byte(connection + ":dm:" + chat))
	return fmt.Sprintf("bot-%x", digest[:8])
}
