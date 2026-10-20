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
	// mounted, when set, is the directory of the one Project this Store
	// knows: the Project's Baton directory as a container sees it.
	mounted string
}

// New creates the storage. batonDir is the Baton state directory (~/.baton).
func New(batonDir string) *Store {
	return &Store{dir: filepath.Join(batonDir, "projects")}
}

// NewMounted creates the storage inside a container, where the Project's Baton
// directory is mounted at projectDir. The name of the Project is not known
// there and not used.
func NewMounted(projectDir string) *Store {
	return &Store{mounted: projectDir}
}

func (s *Store) taskDir(project projectname.Name, task taskid.ID) string {
	if s.mounted != "" {
		return filepath.Join(s.mounted, "handoffs", task.String())
	}
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
	highest, err := s.highest(project, task)
	return highest + 1, err
}

// QueryLatest returns the number and the content of the highest <n>.md of the
// Task; ok is false when it has none.
func (s *Store) QueryLatest(_ context.Context, project projectname.Name, task taskid.ID) (n int, data []byte, ok bool, err error) {
	n, err = s.highest(project, task)
	if err != nil || n == 0 {
		return 0, nil, false, err
	}
	data, err = os.ReadFile(filepath.Join(s.taskDir(project, task), strconv.Itoa(n)+".md"))
	if err != nil {
		return 0, nil, false, err
	}
	return n, data, true, nil
}

// highest is the highest <n>.md there is, 0 for none.
func (s *Store) highest(project projectname.Name, task taskid.ID) (int, error) {
	entries, err := os.ReadDir(s.taskDir(project, task))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
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
	return highest, nil
}

// SaveDraft replaces draft.md atomically, readable by its owner only, and
// returns its path.
func (s *Store) SaveDraft(_ context.Context, project projectname.Name, task taskid.ID, data []byte) (string, error) {
	return s.write(project, task, draftName, data)
}

// SaveHandoff writes <n>.md atomically, readable by its owner only, and returns
// its path.
func (s *Store) SaveHandoff(_ context.Context, project projectname.Name, task taskid.ID, n int, data []byte) (string, error) {
	return s.write(project, task, strconv.Itoa(n)+".md", data)
}

func (s *Store) write(project projectname.Name, task taskid.ID, name string, data []byte) (string, error) {
	dir := s.taskDir(project, task)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
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
