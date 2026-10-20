// Package runtimecompose is the Runtime of Agents that run in a service of a
// Project's own Docker Compose setup.
package runtimecompose

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/domain/runtimebus"
	"github.com/loadkpi/baton/business/domain/syncbus"
	"github.com/loadkpi/baton/business/domain/syncbus/stores/syncdocker"
	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/runtime"
	"github.com/loadkpi/baton/foundation/docker"
)

// Projects lists the registered Projects.
type Projects interface {
	All(ctx context.Context) ([]projectbus.Project, error)
}

// Runtime is the Compose service of the Projects that configure one.
type Runtime struct {
	projects Projects
	client   docker.Client
}

// New creates the compose Runtime over the registered Projects.
func New(projects Projects) *Runtime {
	return &Runtime{projects: projects}
}

var _ runtimebus.Runtime = (*Runtime)(nil)

// Name is "compose".
func (*Runtime) Name() runtime.Runtime { return runtime.Compose }

// Targets are the Profiles inside the containers of the Projects whose config
// has a [projects.<name>.compose] table. Each Profile is synced through Docker
// and is Isolated: a service that cannot be reached fails its own Profiles
// and not the others. The hooks call the binary where the service must mount
// it (runtimebus.ContainerBinary).
//
// Nothing here changes a container or a Compose file. The container's HOME is
// asked once per Project, which is also what makes a service that cannot be
// reached show up as an error of its Profiles (Check repeats it).
func (r *Runtime) Targets(ctx context.Context, cfg configbus.Config) ([]syncbus.Target, []string, error) {
	registered, err := r.projects.All(ctx)
	if err != nil {
		return nil, nil, err
	}
	byName := make(map[projectname.Name]projectbus.Project, len(registered))
	for _, p := range registered {
		byName[p.Name] = p
	}

	var targets []syncbus.Target
	var warnings []string
	for _, ps := range cfg.Projects {
		if ps.Compose == nil {
			continue
		}
		project, ok := byName[ps.Name]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("project %s: skipped, it is not registered; run a Session in its repository once (baton new <path>)", ps.Name))
			continue
		}
		svc := syncdocker.NewService(r.client, ps.Name.String(), composeFiles(cfg, project, ps.Compose.Files), ps.Compose.Service)
		// A service that cannot be reached has no HOME; the Profiles are still
		// listed, and their Check reports why.
		home, _ := svc.Home(ctx)
		for _, p := range ps.Compose.Profiles {
			p.Dir = expandHome(p.Dir, home)
			targets = append(targets, syncbus.Target{
				Profile:    p,
				Runtime:    runtime.Compose,
				Home:       home,
				HookBinary: runtimebus.ContainerBinary,
				Files:      svc.Profile(p.Dir),
				Isolated:   true,
			})
		}
	}
	return targets, warnings, nil
}

// composeFiles are the Compose files as absolute paths: "~/..." is the user's
// home, and a relative file is relative to the Project's root.
func composeFiles(cfg configbus.Config, p projectbus.Project, files []string) []string {
	out := make([]string, len(files))
	for i, f := range files {
		switch {
		case strings.HasPrefix(f, "~/"):
			out[i] = filepath.Join(cfg.Home, f[2:])
		case filepath.IsAbs(f):
			out[i] = f
		default:
			out[i] = filepath.Join(p.Path, f)
		}
	}
	return out
}

// expandHome replaces a leading "~" with the container's HOME. When the HOME
// is unknown the path stays as it is.
func expandHome(dir, home string) string {
	switch {
	case home == "":
		return dir
	case dir == "~":
		return home
	case strings.HasPrefix(dir, "~/"):
		return path.Join(home, dir[2:])
	}
	return dir
}

// Start is built with ticket 17.
func (*Runtime) Start(context.Context, runtimebus.StartRequest) (runtimebus.Tab, error) {
	return runtimebus.Tab{}, runtimebus.ErrNotImplemented
}

// HostPath is built with ticket 17.
func (*Runtime) HostPath(context.Context, projectname.Name, string) (string, error) {
	return "", runtimebus.ErrNotImplemented
}

// ContainerPath is built with ticket 17.
func (*Runtime) ContainerPath(context.Context, projectname.Name, string) (string, error) {
	return "", runtimebus.ErrNotImplemented
}

// Alive is built with ticket 18; until then no Process can be judged.
func (*Runtime) Alive(_ context.Context, procs []runtimebus.Process) ([]runtimebus.Liveness, error) {
	return make([]runtimebus.Liveness, len(procs)), runtimebus.ErrNotImplemented
}
