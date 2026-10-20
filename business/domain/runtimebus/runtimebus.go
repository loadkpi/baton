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

// The two Baton mounts every container Runtime expects (DESIGN.md, D3). The
// containers' own images and Compose files know nothing about them.
const (
	// ContainerBinDir is where ~/.baton/bin is mounted, read-only.
	ContainerBinDir = "/baton/bin"
	// ContainerBinary is the Baton executable in it, the path the hooks of
	// every Profile in a container call.
	ContainerBinary = ContainerBinDir + "/baton"
	// ContainerProject is where the Project's Baton directory is mounted,
	// read-write: the only part of ~/.baton the container sees.
	ContainerProject = "/baton/project"
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
	// Prompt is the first message of the Session, given to the Agent as its
	// initial prompt (both Agents take it as a positional argument, DESIGN.md,
	// D5). Empty for a Session that starts with nothing to say. It should be
	// one line: the Terminal types the command into a shell.
	Prompt string
}

// Process identifies a running Agent as its hook recorded it: the host name
// (the container id in a container), the PID in the PID namespace of its
// Runtime and the start time that guards against a reused PID.
type Process struct {
	Host  string
	PID   int
	Start uint64
}

// Liveness is what a Runtime can say about one Process.
type Liveness int

// The answers of Alive. The zero value is the safe one: a Process whose
// Runtime could not tell is never taken for dead.
const (
	// ProcessUnknown means the Runtime could not judge: the Event is from
	// another machine, docker did not answer, the PID was not recorded.
	ProcessUnknown Liveness = iota
	// ProcessAlive means the PID runs with the recorded start time.
	ProcessAlive
	// ProcessGone means the container, or the PID in it, is gone, or the PID
	// runs with another start time.
	ProcessGone
)

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
	// ContainerPath is the inverse of HostPath: the path under which the
	// Runtime sees a file of the host, so that a message to an Agent in it can
	// name the file. A path the Runtime cannot see is an error.
	ContainerPath(ctx context.Context, project projectname.Name, p string) (string, error)
	// Alive tells for each Process whether the Agent still runs; the answers
	// are in the order of the processes. It takes all the Processes of a
	// check at once so that a container Runtime can ask Docker once for all
	// of them. A Process it cannot judge is ProcessUnknown, and err says why:
	// the answers are valid whatever err is.
	Alive(ctx context.Context, procs []Process) ([]Liveness, error)
}
