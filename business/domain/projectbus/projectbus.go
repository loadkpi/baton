// Package projectbus knows the Projects of Baton: the git repositories it has
// seen Sessions in.
//
// Projects register themselves. The first reader that meets a Session in a git
// repository it does not know registers that repository by its git root.
package projectbus

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/runtime"
)

// Project is a git repository that Baton knows about.
type Project struct {
	Name projectname.Name
	// Path is the git root of the main checkout. The worktrees of the
	// repository are Working copies of the same Project.
	Path string
	// Runtime is the default Runtime: sandbox if the Project has
	// .ai-sandbox/run.sh, host otherwise.
	Runtime runtime.Runtime
}

// Place is where a directory is: a Working copy of a Project.
type Place struct {
	Project Project
	// WorkingCopy is the root of the work tree that holds the directory: the
	// Project's main checkout (Project.Path) or one of its git worktrees.
	WorkingCopy string
}

// Storer is the storage of Project records and the view of the file system the
// domain needs to register one.
type Storer interface {
	// QueryRepo returns the root of the git work tree containing dir and the
	// root of the main checkout of its repository (they differ in a git
	// worktree). ok is false when dir is outside git or does not exist.
	QueryRepo(ctx context.Context, dir string) (workingCopy, main string, ok bool, err error)
	// QuerySandboxScript reports whether root has .ai-sandbox/run.sh.
	QuerySandboxScript(ctx context.Context, root string) (bool, error)
	// QueryAll returns every registered Project.
	QueryAll(ctx context.Context) ([]Project, error)
	// SaveProject writes the record of a Project.
	SaveProject(ctx context.Context, p Project) error
	// Lock takes the lock that serializes registrations and returns the
	// function that releases it.
	Lock(ctx context.Context) (unlock func(), err error)
}

// Bus is the Project domain.
type Bus struct {
	storer Storer
}

// New creates the Project domain over a storage.
func New(storer Storer) *Bus {
	return &Bus{storer: storer}
}

// Resolve returns where a directory is: its Working copy and the Project that
// has it, registering the Project (by the root of its main checkout) when it
// is not known yet. ok is false for a directory outside git, which has none.
func (b *Bus) Resolve(ctx context.Context, dir string) (place Place, ok bool, err error) {
	workingCopy, root, inGit, err := b.storer.QueryRepo(ctx, dir)
	if err != nil || !inGit {
		return Place{}, false, err
	}
	p, err := b.register(ctx, root)
	if err != nil {
		return Place{}, false, err
	}
	return Place{Project: p, WorkingCopy: workingCopy}, true, nil
}

// register returns the Project whose main checkout is at root, creating its
// record when there is none.
func (b *Bus) register(ctx context.Context, root string) (Project, error) {
	known, err := b.storer.QueryAll(ctx)
	if err != nil {
		return Project{}, err
	}
	if p, found := byPath(known, root); found {
		return p, nil
	}

	// Two readers may meet the same new repository at once: the second one
	// looks again under the lock.
	unlock, err := b.storer.Lock(ctx)
	if err != nil {
		return Project{}, err
	}
	defer unlock()

	if known, err = b.storer.QueryAll(ctx); err != nil {
		return Project{}, err
	}
	if p, found := byPath(known, root); found {
		return p, nil
	}

	name, err := freeName(known, root)
	if err != nil {
		return Project{}, err
	}
	hasScript, err := b.storer.QuerySandboxScript(ctx, root)
	if err != nil {
		return Project{}, err
	}
	p := Project{Name: name, Path: root, Runtime: runtime.Host}
	if hasScript {
		p.Runtime = runtime.Sandbox
	}
	if err := b.storer.SaveProject(ctx, p); err != nil {
		return Project{}, err
	}
	return p, nil
}

// All returns every registered Project.
func (b *Bus) All(ctx context.Context) ([]Project, error) {
	return b.storer.QueryAll(ctx)
}

// Named returns the registered Project with the name.
func (b *Bus) Named(ctx context.Context, ref string) (Project, error) {
	name, err := projectname.Parse(ref)
	if err != nil {
		return Project{}, err
	}
	known, err := b.storer.QueryAll(ctx)
	if err != nil {
		return Project{}, err
	}
	for _, p := range known {
		if p.Name == name {
			return p, nil
		}
	}
	return Project{}, fmt.Errorf("unknown project %q; pass the path of a git repository to register it", ref)
}

func byPath(projects []Project, path string) (Project, bool) {
	for _, p := range projects {
		if p.Path == path {
			return p, true
		}
	}
	return Project{}, false
}

// freeName names a new Project after its directory; a name that another
// Project has gets a numeric suffix: app, app-2, app-3.
func freeName(known []Project, root string) (projectname.Name, error) {
	base := sanitize(filepath.Base(root))
	taken := make(map[string]bool, len(known))
	for _, p := range known {
		taken[p.Name.String()] = true
	}
	candidate := base
	for n := 2; taken[candidate]; n++ {
		candidate = base + "-" + strconv.Itoa(n)
	}
	name, err := projectname.Parse(candidate)
	if err != nil {
		return projectname.Name{}, fmt.Errorf("name the Project at %s: %w", root, err)
	}
	return name, nil
}

var unsafeRun = regexp.MustCompile(`[^a-z0-9._-]+`)

// sanitize turns a directory name into a valid Project name.
func sanitize(dir string) string {
	s := unsafeRun.ReplaceAllString(strings.ToLower(dir), "-")
	s = strings.Trim(s, "._-")
	if s == "" {
		return "project"
	}
	return s
}
