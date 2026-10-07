// Package store keeps local state in ~/.local/state/moco/state.json. Access goes through
// Update, which holds an exclusive file lock so the CLI and the daemon don't clobber each other.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/k1tesurfen/moco-cli/internal/api"
)

// State is everything moco-cli remembers locally.
type State struct {
	// Me is the user behind the stored token.
	Me *Me `json:"me,omitempty"`
	// Projects is the cache of GET /projects/assigned.
	Projects *ProjectCache `json:"projects,omitempty"`
}

// Me identifies the logged-in user.
type Me struct {
	Subdomain string `json:"subdomain"`
	UserID    int64  `json:"user_id"`
}

// ProjectCache holds the assigned projects with their fetch time.
type ProjectCache struct {
	FetchedAt time.Time     `json:"fetched_at"`
	Items     []api.Project `json:"items"`
}

// Fresh reports whether the cache is younger than ttl.
func (p *ProjectCache) Fresh(now time.Time, ttl time.Duration) bool {
	return p != nil && now.Sub(p.FetchedAt) < ttl
}

// Dir returns the state directory ($XDG_STATE_HOME/moco or ~/.local/state/moco).
func Dir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "moco")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "moco")
}

// Store reads and writes the state file.
type Store struct{ dir string }

// New returns a store in the default directory.
func New() *Store { return &Store{dir: Dir()} }

// NewAt returns a store in dir (used by tests).
func NewAt(dir string) *Store { return &Store{dir: dir} }

func (s *Store) path() string { return filepath.Join(s.dir, "state.json") }

// Load returns a snapshot of the state.
func (s *Store) Load() (State, error) {
	var st State
	err := s.withLock(func() error {
		var err error
		st, err = s.read()
		return err
	})
	return st, err
}

// Update loads the state, applies fn and writes it back atomically, under an exclusive lock.
func (s *Store) Update(fn func(*State) error) error {
	return s.withLock(func() error {
		st, err := s.read()
		if err != nil {
			return err
		}
		if err := fn(&st); err != nil {
			return err
		}
		return s.write(st)
	})
}

func (s *Store) withLock(fn func() error) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, "state.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock state: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func (s *Store) read() (State, error) {
	var st State
	data, err := os.ReadFile(s.path())
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("parse %s: %w", s.path(), err)
	}
	return st, nil
}

func (s *Store) write(st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, "state-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path())
}
