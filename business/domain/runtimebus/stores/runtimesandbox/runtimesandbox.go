// Package runtimesandbox is the Runtime of Agents that run in an ai-sandbox
// container (ADR 0001).
package runtimesandbox

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/domain/runtimebus"
	"github.com/loadkpi/baton/business/domain/syncbus"
	"github.com/loadkpi/baton/business/domain/syncbus/stores/syncfile"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/runtime"
	"github.com/loadkpi/baton/foundation/docker"
)

// The two Baton mounts of a sandbox container (DESIGN.md, D3), at the paths
// ticket 08 fixed. ai-sandbox does not know them: the print-args wrapper in
// Start adds them.
const (
	// ContainerBinDir is where ~/.baton/bin is mounted, read-only.
	ContainerBinDir = "/baton/bin"
	// ContainerBinary is the Baton executable in it, the path the hooks of
	// every Profile in a sandbox call.
	ContainerBinary = ContainerBinDir + "/baton"
	// ContainerProject is where the Project's Baton directory is mounted,
	// read-write: the only part of ~/.baton the container sees.
	ContainerProject = "/baton/project"
)

// Projects lists the registered Projects.
type Projects interface {
	All(ctx context.Context) ([]projectbus.Project, error)
}

// Runtime is the sandbox.
type Runtime struct {
	projects Projects
	batonDir string
	log      io.Writer
	docker   runtimebus.Containers
}

// New creates the sandbox Runtime over the registered Projects. batonDir is the
// Baton state directory (~/.baton), where it keeps the mounts of the
// containers it started.
func New(projects Projects, batonDir string) *Runtime {
	return &Runtime{projects: projects, batonDir: batonDir, log: io.Discard, docker: docker.Client{}}
}

// WithLog sends what a start has to say to the user (a build of the image, the
// notes of run.sh) to w, which is discarded by default.
func (r *Runtime) WithLog(w io.Writer) *Runtime {
	r.log = w
	return r
}

var _ runtimebus.Runtime = (*Runtime)(nil)

// Name is "sandbox".
func (*Runtime) Name() runtime.Runtime { return runtime.Sandbox }

// Targets are the Claude and Codex Profiles inside the sandboxes of the
// registered Projects whose Runtime is sandbox. Inside the container HOME is
// the sandbox's home directory, .ai-sandbox/home of the Project, so Claude's
// Profile is .claude in it and Codex's is .codex. Their directories may not
// exist yet (ai-sandbox makes only .codex, and the Agents the rest), so only
// the home has to.
func (r *Runtime) Targets(ctx context.Context, cfg configbus.Config) ([]syncbus.Target, []string, error) {
	projects, err := r.projects.All(ctx)
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name.String() < projects[j].Name.String() })

	var targets []syncbus.Target
	var warnings []string
	sandboxed := make(map[projectname.Name]bool)
	for _, p := range projects {
		if p.Runtime != runtime.Sandbox {
			continue
		}
		sandboxed[p.Name] = true
		home := homeOf(p)
		if info, err := os.Stat(home); err != nil || !info.IsDir() {
			warnings = append(warnings, fmt.Sprintf("project %s: skipped, %s does not exist; start its sandbox once with .ai-sandbox/run.sh", p.Name, home))
			continue
		}
		for _, a := range []struct {
			agent agent.Agent
			dir   string
		}{{agent.Claude, ".claude"}, {agent.Codex, ".codex"}} {
			name, err := profilename.Sandbox(p.Name, a.agent)
			if err != nil {
				return nil, nil, fmt.Errorf("name the %s Profile of project %s: %w", a.agent, p.Name, err)
			}
			dir := filepath.Join(home, a.dir)
			targets = append(targets, syncbus.Target{
				Profile:    configbus.Profile{Name: name, Agent: a.agent, Dir: dir, Tags: cfg.SandboxTags(p.Name)},
				Runtime:    runtime.Sandbox,
				Home:       home,
				HookBinary: ContainerBinary,
				Files:      syncfile.NewProfileDir(dir, home),
			})
		}
	}
	// A table in the config that names no Project with a sandbox is most likely
	// a typo, and its tags would silently count for nothing.
	for _, ps := range cfg.Projects {
		if !sandboxed[ps.Name] {
			warnings = append(warnings, fmt.Sprintf("config: [projects.%s] does not name a registered Project with a sandbox; its tags are not used", ps.Name))
		}
	}
	return targets, warnings, nil
}

// Alive asks Docker: a Process is gone with its container, or when its PID in
// the container no longer runs or has another start time. The host name of its
// Events is the container id, which is how the container is found.
func (r *Runtime) Alive(ctx context.Context, procs []runtimebus.Process) ([]runtimebus.Liveness, error) {
	return runtimebus.AliveInContainers(ctx, r.docker, procs)
}

// homeOf is the sandbox's home directory on the host: HOME inside the
// container (/home/sandbox) is a mount of it.
func homeOf(p projectbus.Project) string {
	return filepath.Join(p.Path, ".ai-sandbox", "home")
}
