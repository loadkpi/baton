// Package runtimebus defines the Runtime: where an Agent runs. A Runtime is a
// replaceable part with one interface, so that the commands that start Agents,
// sync Profiles and check on Sessions do not care whether the Agent runs on the
// host, in an ai-sandbox container or in a service of a Compose setup.
package runtimebus

import (
	"context"
	"errors"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/syncbus"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/runtime"
)

// ErrNotImplemented is what a Runtime answers for a part of the interface that
// no ticket has filled in for it yet.
var ErrNotImplemented = errors.New("not implemented yet")

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

// StartRequest asks a Runtime to start an Agent for a Session.
type StartRequest struct {
	// Home is the home directory of the user.
	Home string
	// Project and WorkingCopy say where the Agent works, as host paths.
	Project     projectname.Name
	WorkingCopy string
	// Task is the label of the Task the Session belongs to.
	Task    string
	Agent   agent.Agent
	Profile configbus.Profile
}

// Process identifies a running Agent as its hook recorded it: the host name
// (the container id in a container), the PID in the PID namespace of its
// Runtime and the start time that guards against a reused PID.
type Process struct {
	Host  string
	PID   int
	Start uint64
}

// Runtime is where Agents run.
type Runtime interface {
	// Name is the Runtime as written in files and shown in output.
	Name() runtime.Runtime
	// Start decides how to start an Agent in the Runtime: the tab to open.
	Start(ctx context.Context, r StartRequest) (Tab, error)
	// Targets are the Profiles of the Runtime that `baton sync` renders into,
	// each with the means to write into it. Writing is never assumed to be a
	// plain file write on the host: a Compose Runtime writes through Docker.
	// Warnings are things the user should know that do not stop the sync.
	Targets(ctx context.Context, cfg configbus.Config) (targets []syncbus.Target, warnings []string, err error)
	// HostPath maps a path inside the Runtime (for example the cwd of an Event
	// in a container) to the host path it stands for. A container is a
	// Project's: the mounts of two Projects' containers differ, so the Project
	// says which one the path is seen from.
	HostPath(ctx context.Context, project projectname.Name, p string) (string, error)
	// Alive tells whether the Agent process of a Session still runs.
	Alive(ctx context.Context, p Process) (bool, error)
}
