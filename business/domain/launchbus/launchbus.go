// Package launchbus starts new Sessions: it decides what to run for an Agent
// and hands it to a Terminal that opens a tab for it.
package launchbus

import (
	"context"
	"fmt"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/domain/runtimebus"
	"github.com/loadkpi/baton/business/domain/workbus"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/runtime"
	"github.com/loadkpi/baton/business/types/taskref"
)

// Terminal is where a Session's Agent runs on screen. Guake is the only
// implementation in v1; tmux can plug in later (DESIGN.md, D8).
type Terminal interface {
	OpenTab(ctx context.Context, tab runtimebus.Tab) error
}

// Request asks for a new Session.
type Request struct {
	// Home is the home directory of the user.
	Home string
	// Place is the Working copy to start in.
	Place projectbus.Place
	// Task is the Task to continue or start; the zero value starts a new
	// unnamed one.
	Task    taskref.Ref
	Agent   agent.Agent
	Profile configbus.Profile
}

// Bus is the domain of starting Sessions.
type Bus struct {
	terminal Terminal
	work     *workbus.Bus
	runtimes map[runtime.Runtime]runtimebus.Runtime
}

// New creates the domain over a Terminal, the domain that decides the Task and
// the Runtimes that can start an Agent. A Project whose Runtime is not among
// them (compose, until ticket 17) is refused.
func New(terminal Terminal, work *workbus.Bus, runtimes map[runtime.Runtime]runtimebus.Runtime) *Bus {
	return &Bus{terminal: terminal, work: work, runtimes: runtimes}
}

// Start makes the Task of the request the current one of the Working copy and
// opens a tab that runs the Agent in it. Nothing is changed when the Project's
// Runtime cannot be started yet, when its Sessions may not run in this Working
// copy or when the Working copy has an active Session.
func (b *Bus) Start(ctx context.Context, r Request) error {
	project := r.Place.Project
	rt, ok := b.runtimes[project.Runtime]
	if !ok {
		return fmt.Errorf("project %s runs in the %s Runtime, which baton new cannot start yet", project.Name, project.Runtime)
	}
	// A git worktree stores the host path of its git directory, which does not
	// exist inside a container, and has no sandbox of its own (DESIGN.md, D3).
	if project.Runtime != runtime.Host && r.Place.WorkingCopy != project.Path {
		return fmt.Errorf("project %s runs in the %s Runtime, whose Sessions run only in the main checkout %s, not in the worktree %s",
			project.Name, project.Runtime, project.Path, r.Place.WorkingCopy)
	}
	task, err := b.work.Begin(ctx, r.Place, r.Task)
	if err != nil {
		return err
	}
	tab, err := rt.Start(ctx, runtimebus.StartRequest{
		Home:        r.Home,
		Project:     project.Name,
		WorkingCopy: r.Place.WorkingCopy,
		Task:        task.Label(),
		Agent:       r.Agent,
		Profile:     r.Profile,
	})
	if err != nil {
		return err
	}
	if err := b.terminal.OpenTab(ctx, tab); err != nil {
		return fmt.Errorf("open a tab for %s: %w", tab.Title, err)
	}
	return nil
}
