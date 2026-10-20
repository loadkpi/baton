// Package awayfile keeps the away state in a file: it exists while the user is
// away.
package awayfile

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/loadkpi/baton/foundation/atomicfile"
)

// Store keeps the state in <batonDir>/away.
type Store struct {
	path string
}

// New creates a store in the Baton state directory (~/.baton).
func New(batonDir string) *Store {
	return &Store{path: filepath.Join(batonDir, "away")}
}

// Set creates the file; its content is the time the user went away, for
// whoever looks at it by hand. Only its existence counts.
func (s *Store) Set(_ context.Context, since time.Time) error {
	return atomicfile.Write(s.path, []byte(since.Format(time.RFC3339)+"\n"), 0o600)
}

// Clear removes the file.
func (s *Store) Clear(_ context.Context) error {
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Query tells whether the file exists.
func (s *Store) Query(_ context.Context) (bool, error) {
	_, err := os.Stat(s.path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
