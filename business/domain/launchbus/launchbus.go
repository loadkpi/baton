// Package launchbus starts new Sessions: it decides what to run for an Agent
// and hands it to a Terminal that opens a tab for it.
package launchbus

import (
	"context"
	"fmt"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/domain/workbus"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/runtime"
	"github.com/loadkpi/baton/business/types/taskref"
)

// Tab is what a Terminal opens: a tab in Dir, titled Title, that runs Command.
type Tab struct {
	// Dir is the working directory of the tab.
	Dir string
	// Title is the static title of the tab.
	Title string
	// Command is the program and its arguments, as words. The Terminal
	// decides how to present them to its shell.
	Command []string
}

// Terminal is where a Session's Agent runs on screen. Guake is the only
// implementation in v1; tmux can plug in later (DESIGN.md, D8).
type Terminal interface {
	OpenTab(ctx context.Context, tab Tab) error
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
}

// New creates the domain over a Terminal and the domain that decides the Task.
func New(terminal Terminal, work *workbus.Bus) *Bus {
	return &Bus{terminal: terminal, work: work}
}

// Start makes the Task of the request the current one of the Working copy and
// opens a tab that runs the Agent in it. Nothing is changed when the Project's
// Runtime cannot be started yet or when the Working copy has an active Session.
func (b *Bus) Start(ctx context.Context, r Request) error {
	project := r.Place.Project
	if project.Runtime != runtime.Host {
		return fmt.Errorf("project %s runs in the %s Runtime, which baton new cannot start yet", project.Name, project.Runtime)
	}
	task, err := b.work.Begin(ctx, r.Place, r.Task)
	if err != nil {
		return err
	}
	tab := hostTab(r, task)
	if err := b.terminal.OpenTab(ctx, tab); err != nil {
		return fmt.Errorf("open a tab for %s: %w", tab.Title, err)
	}
	return nil
}
