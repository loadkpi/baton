// Package projectfile is the file storage of the Project domain: one
// directory per Project under ~/.baton/projects with a project.toml in it.
package projectfile

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/runtime"
	"github.com/loadkpi/baton/foundation/atomicfile"
	"github.com/loadkpi/baton/foundation/filelock"
	"github.com/loadkpi/baton/foundation/git"
)

// Store keeps Project records in <baton dir>/projects.
type Store struct {
	dir string
}

// New creates the storage. batonDir is the Baton state directory (~/.baton).
func New(batonDir string) *Store {
	return &Store{dir: filepath.Join(batonDir, "projects")}
}

// fileProject is the document of project.toml. Primitive types only.
type fileProject struct {
	Path    string `toml:"path"`
	Runtime string `toml:"runtime"`
}

func toFileProject(p projectbus.Project) fileProject {
	return fileProject{Path: p.Path, Runtime: p.Runtime.String()}
}

func toBusProject(name string, fp fileProject) (projectbus.Project, error) {
	n, err := projectname.Parse(name)
	if err != nil {
		return projectbus.Project{}, err
	}
	r, err := runtime.Parse(fp.Runtime)
	if err != nil {
		return projectbus.Project{}, err
	}
	return projectbus.Project{Name: n, Path: fp.Path, Runtime: r}, nil
}

// QueryGitRoot returns the root of the work tree containing dir.
func (s *Store) QueryGitRoot(ctx context.Context, dir string) (string, bool, error) {
	return git.TopLevel(ctx, dir)
}

// QuerySandboxScript reports whether root has .ai-sandbox/run.sh.
func (s *Store) QuerySandboxScript(_ context.Context, root string) (bool, error) {
	info, err := os.Stat(filepath.Join(root, ".ai-sandbox", "run.sh"))
	switch {
	case os.IsNotExist(err):
		return false, nil
	case err != nil:
		return false, err
	}
	return info.Mode().IsRegular(), nil
}

// QueryAll reads every <dir>/<name>/project.toml. A directory without one is
// not a Project record (the Project's other files live next to it).
func (s *Store) QueryAll(_ context.Context) ([]projectbus.Project, error) {
	entries, err := os.ReadDir(s.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var projects []projectbus.Project
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		file := filepath.Join(s.dir, e.Name(), "project.toml")
		var fp fileProject
		if _, err := toml.DecodeFile(file, &fp); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		p, err := toBusProject(e.Name(), fp)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		projects = append(projects, p)
	}
	return projects, nil
}

// SaveProject writes <dir>/<name>/project.toml atomically.
func (s *Store) SaveProject(_ context.Context, p projectbus.Project) error {
	dir := filepath.Join(s.dir, p.Name.String())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(toFileProject(p)); err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, "project.toml"), buf.Bytes(), 0o644)
}

// Lock takes the lock that serializes registrations: <dir>/.lock.
func (s *Store) Lock(_ context.Context) (func(), error) {
	return filelock.Acquire(filepath.Join(s.dir, ".lock"))
}
