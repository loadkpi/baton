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
	// Path is the git root of the main checkout.
	Path string
	// Runtime is the default Runtime: sandbox if the Project has
	// .ai-sandbox/run.sh, host otherwise.
	Runtime runtime.Runtime
}

// Storer is the storage of Project records and the view of the file system the
// domain needs to register one.
type Storer interface {
	// QueryGitRoot returns the root of the git work tree containing dir. ok
	// is false when dir is outside git or does not exist.
	QueryGitRoot(ctx context.Context, dir string) (root string, ok bool, err error)
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

// Resolve returns the Project of a directory, registering it when it is not
// known yet. ok is false for a directory outside git, which has no Project.
func (b *Bus) Resolve(ctx context.Context, dir string) (p Project, ok bool, err error) {
	root, inGit, err := b.storer.QueryGitRoot(ctx, dir)
	if err != nil || !inGit {
		return Project{}, false, err
	}

	known, err := b.storer.QueryAll(ctx)
	if err != nil {
		return Project{}, false, err
	}
	if p, found := byPath(known, root); found {
		return p, true, nil
	}

	// Two readers may meet the same new repository at once: the second one
	// looks again under the lock.
	unlock, err := b.storer.Lock(ctx)
	if err != nil {
		return Project{}, false, err
	}
	defer unlock()

	if known, err = b.storer.QueryAll(ctx); err != nil {
		return Project{}, false, err
	}
	if p, found := byPath(known, root); found {
		return p, true, nil
	}

	name, err := freeName(known, root)
	if err != nil {
		return Project{}, false, err
	}
	hasScript, err := b.storer.QuerySandboxScript(ctx, root)
	if err != nil {
		return Project{}, false, err
	}
	p = Project{Name: name, Path: root, Runtime: runtime.Host}
	if hasScript {
		p.Runtime = runtime.Sandbox
	}
	if err := b.storer.SaveProject(ctx, p); err != nil {
		return Project{}, false, err
	}
	return p, true, nil
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
