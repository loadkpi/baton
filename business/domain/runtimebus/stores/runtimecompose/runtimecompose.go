// Package runtimecompose is the Runtime of Agents that run in a service of a
// Project's own Docker Compose setup.
package runtimecompose

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/domain/runtimebus"
	"github.com/loadkpi/baton/business/domain/syncbus"
	"github.com/loadkpi/baton/business/domain/syncbus/stores/syncdocker"
	"github.com/loadkpi/baton/business/types/agent"
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
	cfg      configbus.Config
	client   docker.Client
	log      io.Writer
	// boxes are the containers looked at by HostPath and ContainerPath, once
	// per Project and boxTTL: `baton ls` asks for every Session.
	boxes map[projectname.Name]inspected
}

// New creates the compose Runtime over the registered Projects. cfg says which
// Projects have a service and where; Targets takes its own.
func New(projects Projects, cfg configbus.Config) *Runtime {
	return &Runtime{projects: projects, cfg: cfg, log: io.Discard, boxes: make(map[projectname.Name]inspected)}
}

// WithLog sends what a start has to say to the user (the service coming up,
// the output of docker compose up) to w, which is discarded by default.
func (r *Runtime) WithLog(w io.Writer) *Runtime {
	r.log = w
	return r
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
			if p.Dir == "" && p.Agent == agent.Codex {
				// CODEX_HOME of the service, else ~/.codex. A service that
				// cannot be reached leaves it empty, and Check says why.
				p.Dir, _ = svc.CodexHome(ctx)
			}
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

// Alive asks Docker, as the sandbox does (runtimebus.AliveInContainers): a
// Process is gone with its container, or when its PID in the container no
// longer runs or has another start time.
//
// A container is found by the id its hook recorded, else by a host name that is
// an id. Compose can also give a service a host name of its own (`hostname:`),
// which names no container, and an Event written before the hook recorded ids
// has no id; such a Process is matched to the Project's service container, only
// when all of this holds, and left unjudged otherwise:
//
//   - the Event's host name is the container's configured host name (docker
//     inspect), which is never empty: it is the short id unless set;
//   - the Event is not older than the container: one written before it was
//     created came from an earlier container of the service, which `docker
//     compose up` replaced (the host name is the same, the id is not), so its
//     Session is gone. A recreate that happens while an Agent still writes is
//     the one thing this cannot tell apart; it takes the milliseconds between
//     the new container's creation and the old one's stop.
//
// Inspecting the container costs a `docker compose ps` and a `docker inspect`
// per Project that has such a Process, once per call; never an exec.
func (r *Runtime) Alive(ctx context.Context, procs []runtimebus.Process) ([]runtimebus.Liveness, error) {
	asked := slices.Clone(procs)
	out := make([]runtimebus.Liveness, len(procs))
	var errs []error
	boxes := make(map[projectname.Name]inspected)
	for i, p := range procs {
		if p.NamesContainer() || p.Host == "" || p.Project == (projectname.Name{}) {
			continue
		}
		got, seen := boxes[p.Project]
		if !seen {
			got = r.lookup(ctx, p.Project)
			boxes[p.Project] = got
			if got.err != nil {
				errs = append(errs, got.err)
			}
		}
		switch {
		case got.err != nil || got.box.ID == "" || got.box.Hostname != p.Host:
			// Unknown: some other container, or none, wrote this.
		case p.Seen.Before(got.box.Created):
			out[i] = runtimebus.ProcessGone
		default:
			asked[i].Container = got.box.ID
		}
	}

	verdicts, err := runtimebus.AliveInContainers(ctx, r.client, asked)
	for i, v := range verdicts {
		if out[i] == runtimebus.ProcessUnknown {
			out[i] = v
		}
	}
	return out, errors.Join(append(errs, err)...)
}

// lookup finds the Project's service container for Alive; a service with none
// is a box without an id, not an error.
func (r *Runtime) lookup(ctx context.Context, name projectname.Name) inspected {
	svc, err := r.service(ctx, name)
	if err != nil {
		return inspected{err: err}
	}
	box, ok, err := svc.find(ctx, r.client)
	if err != nil || !ok {
		return inspected{err: err}
	}
	return inspected{box: box}
}
