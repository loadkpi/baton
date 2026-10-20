// Package sessionbus records the Events Agents report and derives the state of
// Sessions from them.
//
// The hook only appends: it calls no git, looks up no Project and interprets
// no payload beyond copying three fields. Everything else (statuses, and later
// notifications and lost detection) is derived by readers from the Event files.
package sessionbus

import (
	"context"
	"time"

	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/runtime"
)

// Event is one line of an Event file: something that happened in a Session, as
// the Agent reported it.
type Event struct {
	TS      time.Time
	Agent   agent.Agent
	Profile profilename.Name
	Runtime runtime.Runtime
	// Host is the machine the hook ran on: the host name, which inside a
	// container is the container id.
	Host string
	// AgentPID and AgentStart identify the Agent's process; AgentStart is
	// its start time in clock ticks since boot, so a reused PID never makes
	// a dead Session look alive.
	AgentPID   int
	AgentStart uint64
	// AgentPIDFallback is set when no ancestor of the hook was recognized as
	// the Agent and AgentPID is the hook's direct parent.
	AgentPIDFallback bool
	SessionID        string
	// Name is the Event's name: hook_event_name of the payload.
	Name string
	CWD  string
	// Payload is what the Agent sent, as one line of JSON.
	Payload []byte
}

// Process is a process of the machine the hook runs on.
type Process struct {
	PID  int
	PPID int
	Comm string
	// ExeBase is the base name of its executable, if known.
	ExeBase string
	Start   uint64
}

// Storer is the storage of Event files.
type Storer interface {
	// SaveEvent appends the Event to the file of its Session, under a lock
	// on the file, creating it readable by its owner only.
	SaveEvent(ctx context.Context, e Event) error
	// QuerySessions lists the Event files.
	QuerySessions(ctx context.Context) ([]SessionRef, error)
	// QueryFirstEvent returns the oldest Event of the Session; ok is false
	// when the file holds none (an empty file, or lines no one can read).
	QueryFirstEvent(ctx context.Context, ref SessionRef) (e Event, ok bool, err error)
	// QueryEventsBackward calls yield with the Session's Events, newest first,
	// until it returns false. Lines that cannot be read are skipped.
	QueryEventsBackward(ctx context.Context, ref SessionRef, yield func(Event) bool) error
}

// SessionRef names the Event file of one Session to the storage.
type SessionRef struct {
	// Project is the Project whose Baton directory holds the file: the Event
	// files of Sessions in a container are written there, through the mount.
	// The zero value is a host Session, whose file is in ~/.baton/sessions.
	Project projectname.Name
	// File is the name of the file.
	File string
}

// Machine is what the hook can learn about the machine it runs on.
type Machine interface {
	// QueryProcess returns the process with the PID.
	QueryProcess(ctx context.Context, pid int) (Process, error)
	// QueryHostname returns the name of the machine.
	QueryHostname(ctx context.Context) (string, error)
}

// Bus is the Session domain.
type Bus struct {
	storer  Storer
	machine Machine
	now     func() time.Time
}

// New creates the Session domain over the Event files and the machine.
func New(storer Storer, machine Machine) *Bus {
	return &Bus{storer: storer, machine: machine, now: time.Now}
}
