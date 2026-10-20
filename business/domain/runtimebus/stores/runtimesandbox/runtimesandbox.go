// Package runtimesandbox is the Runtime of Agents that run in an ai-sandbox
// container (ADR 0001).
package runtimesandbox

import (
	"context"
	"fmt"
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
)

// ContainerBinary is where the container mounts the Baton binary (read-only),
// and the path the hooks of every Profile in a sandbox call. ai-sandbox does not
// know it: the mount is added by the print-args wrapper of ticket 09.
const ContainerBinary = "/baton/bin/baton"

// Projects lists the registered Projects.
type Projects interface {
	All(ctx context.Context) ([]projectbus.Project, error)
}

// Runtime is the sandbox.
type Runtime struct {
	projects Projects
}

// New creates the sandbox Runtime over the registered Projects.
func New(projects Projects) *Runtime { return &Runtime{projects: projects} }

var _ runtimebus.Runtime = (*Runtime)(nil)

// Name is "sandbox".
func (*Runtime) Name() runtime.Runtime { return runtime.Sandbox }

// Start is filled in by ticket 09.
func (*Runtime) Start(context.Context, runtimebus.StartRequest) (runtimebus.Tab, error) {
	return runtimebus.Tab{}, runtimebus.ErrNotImplemented
}

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
		home := filepath.Join(p.Path, ".ai-sandbox", "home")
		if info, err := os.Stat(home); err != nil || !info.IsDir() {
			warnings = append(warnings, fmt.Sprintf("project %s: skipped, %s does not exist; start its sandbox once with .ai-sandbox/run.sh", p.Name, home))
			continue
		}
		for _, a := range []struct {
			agent agent.Agent
			dir   string
		}{{agent.Claude, ".claude"}, {agent.Codex, ".codex"}} {
			name, err := profilename.Parse(p.Name.String() + "." + a.agent.String())
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

// HostPath is filled in by ticket 09.
func (*Runtime) HostPath(context.Context, string) (string, error) {
	return "", runtimebus.ErrNotImplemented
}

// Alive is filled in by ticket 11.
func (*Runtime) Alive(context.Context, runtimebus.Process) (bool, error) {
	return false, runtimebus.ErrNotImplemented
}
