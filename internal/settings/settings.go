// Package settings persists user preferences in the OS config directory.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// Settings is the persisted user configuration.
type Settings struct {
	// Sources are user-added kubeconfig files or folders (absolute paths).
	Sources []string `json:"sources,omitempty"`
	// HiddenSources are default sources the user chose to hide.
	HiddenSources []string `json:"hiddenSources,omitempty"`
	// ActiveContext is the ID of the last selected context.
	ActiveContext string `json:"activeContext,omitempty"`
}

func (s Settings) clone() Settings {
	s.Sources = slices.Clone(s.Sources)
	s.HiddenSources = slices.Clone(s.HiddenSources)
	return s
}

// Store loads and saves Settings at a fixed path. It is safe for concurrent use.
type Store struct {
	path string
	mu   sync.Mutex
	s    Settings
}

// DefaultPath returns <UserConfigDir>/KubeRoot/settings.json.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("settings: locate config dir: %w", err)
	}
	return filepath.Join(dir, "KubeRoot", "settings.json"), nil
}

// Open loads settings from path. A missing file yields empty settings.
func Open(path string) (*Store, error) {
	st := &Store{path: path}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return st, nil
	case err != nil:
		return nil, fmt.Errorf("settings: read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, &st.s); err != nil {
		return nil, fmt.Errorf("settings: parse %s: %w", path, err)
	}
	return st, nil
}

// Get returns a copy of the current settings.
func (st *Store) Get() Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s.clone()
}

// Update applies fn to a copy of the settings and persists the result.
// The in-memory settings change only if the write succeeds.
func (st *Store) Update(fn func(*Settings)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	next := st.s.clone()
	fn(&next)
	if err := writeAtomic(st.path, next); err != nil {
		return err
	}
	st.s = next
	return nil
}

func writeAtomic(path string, s Settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("settings: encode: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("settings: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.json")
	if err != nil {
		return fmt.Errorf("settings: create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op after a successful rename

	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		_ = tmp.Close()
		return fmt.Errorf("settings: chmod: %w", err)
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("settings: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("settings: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("settings: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("settings: replace %s: %w", path, err)
	}
	return nil
}
