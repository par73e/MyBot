package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"reasonix/internal/identitylock"
)

// Repository uses short serialized SQLite transactions. Network/model work is
// never performed while a database transaction or state lock is held.
type Repository struct{ Path string }

func NewRepository(home string) Repository {
	return Repository{Path: filepath.Join(home, "assistant", "state.sqlite3")}
}

func (r Repository) transact(ctx context.Context, action func(*sql.Tx) error) error {
	if r.Path == "" {
		return errors.New("assistant database path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o700); err != nil {
		return err
	}
	lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	release, err := identitylock.Acquire(lockCtx, r.Path+".lock")
	if err != nil {
		return err
	}
	defer release()
	db, err := sql.Open("sqlite", r.Path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, "PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;"); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS assistant_meta(version INTEGER NOT NULL); INSERT INTO assistant_meta SELECT 1 WHERE NOT EXISTS(SELECT 1 FROM assistant_meta); CREATE TABLE IF NOT EXISTS assistant_entities(kind TEXT NOT NULL,id TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(kind,id));"); err != nil {
		return err
	}
	if err = os.Chmod(r.Path, 0o600); err != nil {
		return err
	}
	var version int
	if err = db.QueryRowContext(ctx, "SELECT version FROM assistant_meta").Scan(&version); err != nil {
		return err
	}
	if version != 1 {
		return fmt.Errorf("unsupported assistant database schema %d", version)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = action(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func loadEntities[T any](ctx context.Context, tx *sql.Tx, kind string) ([]T, error) {
	rows, err := tx.QueryContext(ctx, "SELECT payload FROM assistant_entities WHERE kind=? ORDER BY rowid", kind)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []T{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item T
		if err = json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, fmt.Errorf("invalid %s state: %w", kind, err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func saveEntity(ctx context.Context, tx *sql.Tx, kind, id string, item any) error {
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	if len(raw) > 2<<20 {
		return errors.New("assistant entity exceeds 2 MiB")
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO assistant_entities(kind,id,payload) VALUES(?,?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload", kind, id, string(raw))
	return err
}
