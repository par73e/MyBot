package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Notification is a durable outbox entry. "sending" after a process crash is
// uncertain and must never be replayed automatically. "sent" retries only the
// canonical conversation import, not the platform delivery.
type Notification struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Text      string    `json:"text"`
	At        time.Time `json:"at"`
	State     string    `json:"state"`
}

func (r Repository) PrepareNotification(ctx context.Context, owner string, n Notification) (Notification, error) {
	err := r.transact(ctx, func(tx *sql.Tx) error {
		if err := dailyTables(ctx, tx); err != nil {
			return err
		}
		var raw string
		err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_notifications WHERE owner=? AND id=?", owner, n.ID).Scan(&raw)
		if err == nil {
			var stored Notification
			if err := json.Unmarshal([]byte(raw), &stored); err != nil {
				return err
			}
			if stored.SessionID != n.SessionID || stored.Text != n.Text {
				return errors.New("notification identity conflict")
			}
			n = stored
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		n.State = "prepared"
		body, err := json.Marshal(n)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO assistant_notifications(owner,id,payload) VALUES(?,?,?)", owner, n.ID, string(body))
		return err
	})
	return n, err
}

func (r Repository) NotificationState(ctx context.Context, owner string, n Notification, state string) error {
	n.State = state
	return r.transact(ctx, func(tx *sql.Tx) error {
		if err := dailyTables(ctx, tx); err != nil {
			return err
		}
		raw, err := json.Marshal(n)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, "UPDATE assistant_notifications SET payload=? WHERE owner=? AND id=?", string(raw), owner, n.ID)
		if err != nil {
			return err
		}
		count, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return errors.New("notification outbox entry missing")
		}
		return nil
	})
}
