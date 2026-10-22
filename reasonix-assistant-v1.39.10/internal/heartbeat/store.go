package heartbeat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"reasonix/internal/fileutil"
	"reasonix/internal/identitylock"
)

type document struct {
	Version int    `json:"schemaVersion"`
	Tasks   []Task `json:"tasks"`
}

// TaskStore abstracts persistent jobs without coupling dispatch to JSON or SQL.
type TaskStore interface {
	List(context.Context) ([]Task, error)
	Update(context.Context, string, func(*Task) error) error
	LeasePath() string
}

// LeasePath identifies the shared cross-process execution lock namespace.
func (s Store) LeasePath() string { return s.Path + ".runner.lock" }

// Store owns the headless task file, separate from desktop heartbeat state.
type Store struct{ Path string }

// List returns a consistent snapshot without changing task state.
func (s Store) List(ctx context.Context) ([]Task, error) {
	var tasks []Task
	err := s.locked(ctx, false, func(d *document) error {
		tasks = d.Tasks
		return nil
	})
	return tasks, err
}

// Edit serializes read-modify-write across processes and publishes atomically.
func (s Store) Edit(ctx context.Context, edit func(*[]Task) error) error {
	return s.locked(ctx, true, func(d *document) error { return edit(&d.Tasks) })
}

func (s Store) locked(ctx context.Context, write bool, action func(*document) error) error {
	if s.Path == "" {
		return errors.New("heartbeat store path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	lockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	release, err := identitylock.Acquire(lockCtx, s.Path+".lock")
	if err != nil {
		return err
	}
	defer release()
	d, err := s.read()
	if err != nil {
		return err
	}
	if err := action(&d); err != nil || !write {
		return err
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	if len(b) > 8<<20 {
		return errors.New("heartbeat store exceeds 8 MiB; remove old task history before retrying")
	}
	return fileutil.AtomicWriteFileStrict(s.Path, b, 0o600)
}

func (s Store) read() (document, error) {
	d := document{Version: 1, Tasks: []Task{}}
	info, err := os.Stat(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	if info.Size() > 8<<20 {
		return d, errors.New("heartbeat store exceeds 8 MiB")
	}
	b, err := os.ReadFile(s.Path)
	if err != nil {
		return d, err
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return d, fmt.Errorf("read heartbeat store: %w", err)
	}
	if d.Version != 1 {
		return d, fmt.Errorf("unsupported heartbeat schema %d", d.Version)
	}
	seen := map[string]bool{}
	for _, task := range d.Tasks {
		if err := task.Validate(); err != nil {
			return d, fmt.Errorf("task %q: %w", task.ID, err)
		}
		if seen[task.ID] {
			return d, fmt.Errorf("duplicate heartbeat task %q", task.ID)
		}
		seen[task.ID] = true
	}
	return d, nil
}

// Add persists a validated new task without replacing an existing identity.
func (s Store) Add(ctx context.Context, task Task) error {
	if err := task.Validate(); err != nil {
		return err
	}
	if err := task.validateWorkspace(); err != nil {
		return err
	}
	return s.Edit(ctx, func(tasks *[]Task) error {
		for _, existing := range *tasks {
			if existing.ID == task.ID {
				return fmt.Errorf("task %q already exists", task.ID)
			}
		}
		*tasks = append(*tasks, task)
		return nil
	})
}

// Update changes one current task; deletion while work runs is never resurrected.
func (s Store) Update(ctx context.Context, id string, edit func(*Task) error) error {
	return s.Edit(ctx, func(tasks *[]Task) error {
		for i := range *tasks {
			if (*tasks)[i].ID == id {
				return edit(&(*tasks)[i])
			}
		}
		return fmt.Errorf("heartbeat task %q not found", id)
	})
}
