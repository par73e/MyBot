package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"reasonix/internal/heartbeat"
)

// JobStore implements Heartbeat persistence over the same SQLite transaction
// boundary as todos, so an acknowledged reminder cannot lack its trigger.
type JobStore struct{ Repository Repository }

func (s JobStore) LeasePath() string { return s.Repository.Path + ".runner" }

func (s JobStore) List(ctx context.Context) ([]heartbeat.Task, error) {
	var jobs []heartbeat.Task
	err := s.Repository.transact(ctx, func(tx *sql.Tx) error {
		var err error
		jobs, err = loadEntities[heartbeat.Task](ctx, tx, "job")
		return err
	})
	return jobs, err
}

func (s JobStore) Edit(ctx context.Context, edit func(*[]heartbeat.Task) error) error {
	return s.Repository.transact(ctx, func(tx *sql.Tx) error {
		jobs, err := loadEntities[heartbeat.Task](ctx, tx, "job")
		if err != nil {
			return err
		}
		if err = edit(&jobs); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM assistant_entities WHERE kind='job'"); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, job := range jobs {
			if seen[job.ID] {
				return fmt.Errorf("duplicate job ID %q", job.ID)
			}
			seen[job.ID] = true
			if err = job.Validate(); err != nil {
				return err
			}
			if err = saveEntity(ctx, tx, "job", job.ID, job); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s JobStore) Update(ctx context.Context, id string, edit func(*heartbeat.Task) error) error {
	return s.Repository.transact(ctx, func(tx *sql.Tx) error {
		var raw string
		if err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='job' AND id=?", id).Scan(&raw); err != nil {
			return fmt.Errorf("load assistant job: %w", err)
		}
		var job heartbeat.Task
		if err := json.Unmarshal([]byte(raw), &job); err != nil {
			return err
		}
		if err := edit(&job); err != nil {
			return err
		}
		if job.ID != id {
			return fmt.Errorf("job identity cannot change")
		}
		if err := job.Validate(); err != nil {
			return err
		}
		return saveEntity(ctx, tx, "job", id, job)
	})
}

func (s JobStore) Add(ctx context.Context, job heartbeat.Task) error {
	if err := job.Validate(); err != nil {
		return err
	}
	return s.Edit(ctx, func(jobs *[]heartbeat.Task) error {
		for _, item := range *jobs {
			if item.ID == job.ID {
				return fmt.Errorf("job %q already exists", job.ID)
			}
		}
		*jobs = append(*jobs, job)
		return nil
	})
}
