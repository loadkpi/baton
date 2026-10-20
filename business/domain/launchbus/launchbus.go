// Package launchbus starts new Sessions: it decides what to run for an Agent
// and hands it to a Terminal that opens a tab for it.
package launchbus

import (
	"context"
	"fmt"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/runtime"
	"github.com/loadkpi/baton/business/types/taskname"
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
	Home    string
	Project projectbus.Project
	// Task is the name that goes into the tab title; the zero value means
	// none. Tasks themselves arrive with ticket 07.
	Task    taskname.Name
	Agent   agent.Agent
	Profile configbus.Profile
}

// Bus is the domain of starting Sessions.
type Bus struct {
	terminal Terminal
}

// New creates the domain over a Terminal.
func New(terminal Terminal) *Bus {
	return &Bus{terminal: terminal}
}

// Start opens a tab that runs the Agent of the request.
func (b *Bus) Start(ctx context.Context, r Request) error {
	var tab Tab
	switch r.Project.Runtime {
	case runtime.Host:
		tab = hostTab(r)
	default:
		return fmt.Errorf("project %s runs in the %s Runtime, which baton new cannot start yet", r.Project.Name, r.Project.Runtime)
	}
	if err := b.terminal.OpenTab(ctx, tab); err != nil {
		return fmt.Errorf("open a tab for %s: %w", tab.Title, err)
	}
	return nil
}
