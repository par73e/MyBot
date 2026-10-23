package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/fileutil"
	"reasonix/internal/identitylock"
)

// Memory is readable Markdown with JSON (also valid YAML) front matter.
type Memory struct {
	ID        string     `json:"id"`
	Revision  int        `json:"revision"`
	Subject   string     `json:"subject"`
	Body      string     `json:"body"`
	Kind      string     `json:"kind"`
	StartsAt  time.Time  `json:"starts_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Archived  bool       `json:"archived,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type MemoryRequest struct {
	Action          string `json:"action"`
	ID              string `json:"id,omitempty"`
	Revision        int    `json:"revision,omitempty"`
	Subject         string `json:"subject,omitempty"`
	Body            string `json:"body,omitempty"`
	Kind            string `json:"kind,omitempty"`
	StartsAt        string `json:"starts_at,omitempty"`
	ExpiresAt       string `json:"expires_at,omitempty"`
	IncludeInactive bool   `json:"include_inactive,omitempty"`
}

type Memories struct {
	Home string
	Now  func() time.Time
}

func (m Memories) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}
func (m Memories) dir() string { return filepath.Join(m.Home, "assistant", "memory") }
func (m Memory) Active(now time.Time) bool {
	return m.Visible(now) && !now.Before(m.StartsAt)
}

// Visible includes known future facts from the moment they are saved.
// StartsAt controls temporal applicability, not when the Agent can learn a plan.
func (m Memory) Visible(now time.Time) bool {
	return !m.Archived && (m.ExpiresAt == nil || now.Before(*m.ExpiresAt))
}

func (m Memories) Apply(ctx context.Context, r MemoryRequest) ([]Memory, error) {
	if err := os.MkdirAll(m.dir(), 0o700); err != nil {
		return nil, err
	}
	lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	release, err := identitylock.Acquire(lockCtx, filepath.Join(m.dir(), ".lock"))
	if err != nil {
		return nil, err
	}
	defer release()
	all, err := m.read()
	if err != nil {
		return nil, err
	}
	if r.Action == "list" {
		out := []Memory{}
		for _, v := range all {
			if r.IncludeInactive || v.Visible(m.now()) {
				out = append(out, v)
			}
		}
		return out, nil
	}
	if r.Action == "create" {
		return m.create(r, all)
	}
	for _, v := range all {
		if v.ID != r.ID {
			continue
		}
		if r.Action == "get" {
			return []Memory{v}, nil
		}
		if r.Revision != v.Revision {
			return nil, errors.New("memory revision changed; get the current entry before updating")
		}
		switch r.Action {
		case "archive":
			v.Archived = true
		case "update":
			if r.Subject != "" {
				v.Subject = strings.TrimSpace(r.Subject)
			}
			if r.Body != "" {
				v.Body = strings.TrimSpace(r.Body)
			}
			if r.Kind != "" {
				v.Kind = r.Kind
			}
			if err = m.configure(&v, r); err != nil {
				return nil, err
			}
		default:
			return nil, errors.New("unknown memory action")
		}
		v.Revision++
		v.UpdatedAt = m.now()
		if err = m.write(v, all); err != nil {
			return nil, err
		}
		return []Memory{v}, nil
	}
	return nil, errors.New("memory not found")
}

func (m Memories) configure(v *Memory, r MemoryRequest) error {
	if r.StartsAt != "" {
		at, err := timestamp(r.StartsAt)
		if err != nil {
			return err
		}
		v.StartsAt = at
	}
	if r.ExpiresAt != "" {
		at, err := timestamp(r.ExpiresAt)
		if err != nil {
			return err
		}
		v.ExpiresAt = &at
	}
	if v.Kind == "long" {
		v.ExpiresAt = nil
	}
	if v.Kind != "long" && v.Kind != "short" {
		return errors.New("memory kind must be long or short")
	}
	if v.Kind == "short" && (v.ExpiresAt == nil || !v.ExpiresAt.After(v.StartsAt) || !v.ExpiresAt.After(m.now())) {
		return errors.New("short memory requires a future expires_at later than starts_at")
	}
	if v.Subject == "" || v.Body == "" || len(v.Subject) > 256 || len(v.Body) > 6000 {
		return errors.New("memory requires subject <=256 bytes and body <=6000 bytes")
	}
	return nil
}

func (m Memories) read() ([]Memory, error) {
	entries, err := os.ReadDir(m.dir())
	if err != nil {
		return nil, err
	}
	out := []Memory{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "MEMORY.md" || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > 10000 {
			return nil, errors.New("invalid memory file")
		}
		raw, err := os.ReadFile(filepath.Join(m.dir(), entry.Name()))
		if err != nil {
			return nil, err
		}
		parts := strings.SplitN(string(raw), "\n---\n", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "---\n") {
			return nil, fmt.Errorf("invalid memory Markdown %s", entry.Name())
		}
		var v Memory
		if err = json.Unmarshal([]byte(strings.TrimPrefix(parts[0], "---\n")), &v); err != nil {
			return nil, fmt.Errorf("invalid memory metadata: %w", err)
		}
		if entry.Name() != v.ID+".md" || len(v.ID) != 24 {
			return nil, errors.New("memory ID does not match filename")
		}
		v.Body = strings.TrimSpace(parts[1])
		out = append(out, v)
	}
	return out, nil
}

func (m Memories) write(v Memory, all []Memory) error {
	total := len(v.Body)
	for _, old := range all {
		if old.ID != v.ID && old.Visible(m.now()) {
			total += len(old.Body)
		}
	}
	if total > 96000 {
		return errors.New("visible memory exceeds 96 KB; consolidate memories first")
	}
	meta := v
	meta.Body = ""
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFileStrict(filepath.Join(m.dir(), v.ID+".md"), []byte("---\n"+string(raw)+"\n---\n\n"+v.Body+"\n"), 0o600)
}

func (m Memories) create(r MemoryRequest, all []Memory) ([]Memory, error) {
	var err error

	if len(all) >= 1000 {
		return nil, errors.New("memory capacity reached; archive and export old entries before adding more")
	}
	for _, v := range all {
		if !v.Archived && v.Subject == strings.TrimSpace(r.Subject) && v.Body == strings.TrimSpace(r.Body) && v.Kind == r.Kind && (r.StartsAt == "" || r.StartsAt == v.StartsAt.Format(time.RFC3339)) && (v.ExpiresAt == nil || v.ExpiresAt.After(m.now())) && (r.ExpiresAt == "" && v.ExpiresAt == nil || v.ExpiresAt != nil && r.ExpiresAt == v.ExpiresAt.Format(time.RFC3339)) {
			return []Memory{v}, nil
		}
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	v := Memory{ID: id, Revision: 1, Subject: strings.TrimSpace(r.Subject), Body: strings.TrimSpace(r.Body), Kind: r.Kind, StartsAt: m.now(), UpdatedAt: m.now()}
	if err = m.configure(&v, r); err != nil {
		return nil, err
	}
	if err = m.write(v, all); err != nil {
		return nil, err
	}
	return []Memory{v}, nil

}
