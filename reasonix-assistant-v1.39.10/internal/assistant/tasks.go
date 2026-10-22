package assistant

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"reasonix/internal/heartbeat"
)

// Tasks binds all schedule destinations and workspace roots to trusted host data.
type Tasks struct {
	Repository Repository
	Options    Options
	Now        func() time.Time
}

func (s Tasks) clock() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Apply performs one validated personal-state operation.
func (s Tasks) Apply(ctx context.Context, in TaskRequest) (any, error) {
	if in.At != "" && in.Interval != "" {
		return nil, errors.New("set exactly one of at or interval")
	}
	if in.Action == "list" {
		return s.List(ctx, in.IncludeClosed)
	}
	if in.Action == "create" {
		return s.create(ctx, in)
	}
	if in.ID == "" {
		return nil, errors.New("id is required; inspect assistant_tasks list first")
	}
	var result TaskView
	err := s.Repository.transact(ctx, func(tx *sql.Tx) error {
		todos, err := loadEntities[Todo](ctx, tx, "todo")
		if err != nil {
			return err
		}
		jobs, err := loadEntities[heartbeat.Task](ctx, tx, "job")
		if err != nil {
			return err
		}
		for _, todo := range todos {
			if todo.ID != in.ID {
				continue
			}
			var job *heartbeat.Task
			for i := range jobs {
				if jobs[i].ID == todo.JobID {
					job = &jobs[i]
					break
				}
			}
			if in.Action != "get" {
				if in.Revision != todo.Revision {
					return fmt.Errorf("revision conflict: current revision is %d; reload before changing", todo.Revision)
				}
				if err = s.change(&todo, job, in); err != nil {
					return err
				}
				if in.Kind != "" && in.Kind != todo.Kind {
					return errors.New("task kind cannot change; create a new task")
				}
				size := 0
				for _, item := range todos {
					if item.ID == todo.ID {
						item = todo
					}
					if item.Status != "done" && item.Status != "cancelled" {
						size += len(item.Title) + len(item.Body)
					}
				}
				if size > 96000 {
					return errors.New("active tasks exceed 96 KB")
				}
				todo.Revision++
				todo.UpdatedAt = s.clock().UTC()
				if err = saveEntity(ctx, tx, "todo", todo.ID, todo); err != nil {
					return err
				}
				if job != nil {
					if err = saveEntity(ctx, tx, "job", job.ID, *job); err != nil {
						return err
					}
				}
			}
			result = TaskView{Todo: todo, Schedule: job}
			return nil
		}
		return fmt.Errorf("todo %q not found", in.ID)
	})
	return result, err
}

func (s Tasks) List(ctx context.Context, closed bool) ([]TaskView, error) {
	out := []TaskView{}
	err := s.Repository.transact(ctx, func(tx *sql.Tx) error {
		todos, err := loadEntities[Todo](ctx, tx, "todo")
		if err != nil {
			return err
		}
		jobs, err := loadEntities[heartbeat.Task](ctx, tx, "job")
		if err != nil {
			return err
		}
		for _, todo := range todos {
			if !closed && (todo.Status == "done" || todo.Status == "cancelled") {
				continue
			}
			view := TaskView{Todo: todo}
			for i := range jobs {
				if jobs[i].ID == todo.JobID {
					view.Schedule = &jobs[i]
					break
				}
			}
			out = append(out, view)
		}
		return nil
	})
	return out, err
}

func (s Tasks) create(ctx context.Context, in TaskRequest) (TaskView, error) {
	in.Title = strings.TrimSpace(in.Title)
	in.Body = strings.TrimSpace(in.Body)
	if in.Title == "" || len(in.Title) > 512 || len(in.Body) > 16000 {
		return TaskView{}, errors.New("title is required (max 512 bytes); body max 16000 bytes")
	}
	if in.Kind == "" {
		if in.At != "" || in.Interval != "" {
			in.Kind = "reminder"
		} else {
			in.Kind = "todo"
		}
	}
	if in.Kind != "todo" && in.Kind != "reminder" && in.Kind != "automation" {
		return TaskView{}, errors.New("kind must be todo, reminder or automation")
	}
	id, err := newID()
	if err != nil {
		return TaskView{}, err
	}
	if _, err := timestamp(in.DueAt); err != nil {
		return TaskView{}, err
	}
	now := s.clock().UTC()
	todo := Todo{DueAt: in.DueAt, ID: id, Revision: 1, Title: in.Title, Body: in.Body, Kind: in.Kind, Status: "open", CreatedAt: now, UpdatedAt: now}
	view := TaskView{Todo: todo}
	if in.Kind != "todo" {
		job, err := s.makeJob(id, in, now)
		if err != nil {
			return TaskView{}, err
		}
		view.JobID = job.ID
		view.Schedule = &job
	} else if in.At != "" || in.Interval != "" {
		return TaskView{}, errors.New("use reminder or automation when specifying a schedule")
	}
	err = s.saveNewTask(ctx, &view)
	return view, err
}

func equivalentSchedule(a, b *heartbeat.Task) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.At == b.At && a.Interval == b.Interval && a.EndsAt == b.EndsAt && a.Timezone == b.Timezone
}

func (s Tasks) makeJob(id string, in TaskRequest, now time.Time) (heartbeat.Task, error) {
	if s.Options.Target == nil {
		return heartbeat.Task{}, errors.New("notification destination is not configured; no reminder was created")
	}
	zone := in.Timezone
	if zone == "" {
		zone = "Asia/Shanghai"
	}
	body := in.Body
	if body == "" {
		body = in.Title
	}
	job := heartbeat.Task{ID: "todo-" + id, Title: in.Title, Prompt: body, At: in.At, Interval: in.Interval, Timezone: zone, EndsAt: in.EndsAt, Enabled: true, WorkspaceRoot: s.Options.Workspace, Model: s.Options.Model, ApprovalMode: "read-only", Kind: "message", NewConversationEachRun: true, CreatedAt: now.UnixMilli(), Target: s.Options.Target}
	if in.Kind == "automation" {
		job.Kind = "agent"
		job.DecideNotification = true
	}
	if err := checkFutureSchedule(job, now); err != nil {
		return job, err
	}
	return job, job.Validate()
}

func checkFutureSchedule(job heartbeat.Task, now time.Time) error {
	at, err := timestamp(job.At)
	if err != nil {
		return err
	}
	end, err := timestamp(job.EndsAt)
	if err != nil {
		return err
	}
	if !at.IsZero() && !at.After(now) {
		return errors.New("reminder time must be in the future")
	}
	if !end.IsZero() && (!end.After(now) || (!at.IsZero() && !end.After(at))) {
		return errors.New("ends_at must be after now and the reminder time")
	}
	return nil
}

func (s Tasks) saveNewTask(ctx context.Context, view *TaskView) error {
	return s.Repository.transact(ctx, func(tx *sql.Tx) error {
		todos, err := loadEntities[Todo](ctx, tx, "todo")
		if err != nil {
			return err
		}
		activeSize := len(view.Title) + len(view.Body)
		for _, v := range todos {
			if v.Status != "done" && v.Status != "cancelled" {
				activeSize += len(v.Title) + len(v.Body)
			}
		}
		if activeSize > 96000 {
			return errors.New("active tasks exceed 96 KB; complete or consolidate old obligations")
		}
		if len(todos) >= 2000 {
			return errors.New("personal todo capacity reached")
		}
		jobs, err := loadEntities[heartbeat.Task](ctx, tx, "job")
		if err != nil {
			return err
		}
		// Stable active-content deduplication handles repeated model tool calls.
		for _, old := range todos {
			if old.Status == "done" || old.Status == "cancelled" || old.Title != view.Title || old.Body != view.Body || old.Kind != view.Kind || old.DueAt != view.DueAt {
				continue
			}
			var oldJob *heartbeat.Task
			for i := range jobs {
				if jobs[i].ID == old.JobID {
					oldJob = &jobs[i]
					break
				}
			}
			if equivalentSchedule(oldJob, view.Schedule) {
				*view = TaskView{Todo: old, Schedule: oldJob}
				return nil
			}
		}
		if err = saveEntity(ctx, tx, "todo", view.ID, view.Todo); err != nil {
			return err
		}
		if view.Schedule != nil {
			return saveEntity(ctx, tx, "job", view.Schedule.ID, *view.Schedule)
		}
		return nil
	})
}
