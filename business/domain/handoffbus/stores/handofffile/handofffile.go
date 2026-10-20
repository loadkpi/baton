// Package handofffile is the file storage of the Handoff domain: the Handoffs
// and the draft of a Task in <project>/handoffs/<task>.
package handofffile

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/taskid"
	"github.com/loadkpi/baton/foundation/atomicfile"
	"github.com/loadkpi/baton/foundation/filelock"
)

const (
	draftName = "draft.md"
	lockName  = ".lock"
)

// Store keeps the Handoffs of every Project under <baton dir>/projects.
type Store struct {
	dir string
}

// New creates the storage. batonDir is the Baton state directory (~/.baton).
func New(batonDir string) *Store {
	return &Store{dir: filepath.Join(batonDir, "projects")}
}

func (s *Store) taskDir(project projectname.Name, task taskid.ID) string {
	return filepath.Join(s.dir, project.String(), "handoffs", task.String())
}

// Lock serializes the writers of one Task's Handoffs.
func (s *Store) Lock(_ context.Context, project projectname.Name, task taskid.ID) (unlock func(), err error) {
	return filelock.Acquire(filepath.Join(s.taskDir(project, task), lockName))
}

// QueryDraft returns the content of draft.md; ok is false when there is none.
func (s *Store) QueryDraft(_ context.Context, project projectname.Name, task taskid.ID) (text string, ok bool, err error) {
	data, err := os.ReadFile(filepath.Join(s.taskDir(project, task), draftName))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(data), true, nil
}

// QueryNext returns the number the next Handoff of the Task gets: one more than
// the highest <n>.md there is, 1 for the first.
func (s *Store) QueryNext(_ context.Context, project projectname.Name, task taskid.ID) (int, error) {
	entries, err := os.ReadDir(s.taskDir(project, task))
	if errors.Is(err, fs.ErrNotExist) {
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	highest := 0
	for _, e := range entries {
		name, isMarkdown := strings.CutSuffix(e.Name(), ".md")
		if n, err := strconv.Atoi(name); isMarkdown && err == nil && e.Type().IsRegular() {
			highest = max(highest, n)
		}
	}
	return highest + 1, nil
}

// SaveHandoff writes <n>.md atomically, readable by its owner only, and returns
// its path.
func (s *Store) SaveHandoff(_ context.Context, project projectname.Name, task taskid.ID, n int, data []byte) (string, error) {
	dir := s.taskDir(project, task)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, strconv.Itoa(n)+".md")
	if err := atomicfile.Write(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// DeleteDraft removes draft.md; a missing one is not an error.
func (s *Store) DeleteDraft(_ context.Context, project projectname.Name, task taskid.ID) error {
	err := os.Remove(filepath.Join(s.taskDir(project, task), draftName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
