package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"awssession/internal/domain"
)

const appDirName = "awssession"
const workspaceFile = "workspace.json"

// Store loads and saves the workspace JSON file.
type Store struct {
	mu   sync.Mutex
	path string
	ws   domain.Workspace
	// recoveredFrom is the backup path if a corrupt file was set aside on load.
	recoveredFrom string
}

// RecoveredFrom returns the backup path of a corrupt workspace file that was
// replaced by a fresh workspace on load, or "" if nothing was recovered.
func (s *Store) RecoveredFrom() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recoveredFrom
}

// NewStore creates a store under the OS app data directory.
func NewStore() (*Store, error) {
	dir, err := appDataDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create app data dir: %w", err)
	}
	s := &Store{path: filepath.Join(dir, workspaceFile)}
	if err := s.Load(); err != nil {
		return nil, err
	}
	return s, nil
}

// NewStoreAt is useful for tests.
func NewStoreAt(path string) (*Store, error) {
	s := &Store{path: path}
	if err := s.Load(); err != nil {
		return nil, err
	}
	return s, nil
}

func appDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config dir: %w", err)
	}
	return filepath.Join(base, appDirName), nil
}

// Load reads the workspace from disk, or initializes defaults.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.ws = domain.NewWorkspace()
			return s.saveLocked()
		}
		return fmt.Errorf("read workspace: %w", err)
	}
	var ws domain.Workspace
	if err := json.Unmarshal(data, &ws); err != nil {
		// Keep the unreadable file for manual recovery and start fresh rather
		// than leaving the app unable to start.
		backup := fmt.Sprintf("%s.corrupt-%d", s.path, time.Now().Unix())
		if rerr := os.Rename(s.path, backup); rerr != nil {
			return fmt.Errorf("parse workspace: %w (and could not back it up: %v)", err, rerr)
		}
		s.recoveredFrom = backup
		s.ws = domain.NewWorkspace()
		return s.saveLocked()
	}
	if ws.Settings.DefaultRegion == "" {
		ws.Settings = domain.DefaultSettings()
	}
	if ws.Integrations == nil {
		ws.Integrations = []domain.Integration{}
	}
	if ws.Sessions == nil {
		ws.Sessions = []domain.Session{}
	}
	if ws.ManagedProfiles == nil {
		ws.ManagedProfiles = []string{}
	}
	s.ws = ws
	return nil
}

// Get returns a copy of the current workspace.
func (s *Store) Get() domain.Workspace {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneWorkspace(s.ws)
}

// Update applies a mutation and persists.
func (s *Store) Update(fn func(ws *domain.Workspace) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := cloneWorkspace(s.ws)
	if err := fn(&s.ws); err != nil {
		s.ws = snapshot
		return err
	}
	if err := s.saveLocked(); err != nil {
		// Keep memory consistent with what is on disk.
		s.ws = snapshot
		return err
	}
	return nil
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.ws, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal workspace: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write workspace tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename workspace: %w", err)
	}
	return nil
}

func cloneWorkspace(ws domain.Workspace) domain.Workspace {
	out := ws
	out.Integrations = append([]domain.Integration(nil), ws.Integrations...)
	out.Sessions = append([]domain.Session(nil), ws.Sessions...)
	out.ManagedProfiles = append([]string(nil), ws.ManagedProfiles...)
	return out
}
