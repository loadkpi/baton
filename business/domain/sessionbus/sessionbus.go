// Package sessionbus records the Events Agents report and derives the state of
// Sessions from them.
//
// The hook only appends: it calls no git, looks up no Project and interprets
// no payload beyond copying three fields and masking secrets. Everything else (statuses, and later
// notifications and lost detection) is derived by readers from the Event files.
package sessionbus

import (
	"context"
	"time"

	"github.com/loadkpi/baton/business/domain/runtimebus"
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
	// Container is the full id of the container the hook ran in, when it
	// could be found; empty on the host and where the kernel hides it. A
	// container with a host name of its own is found by it.
	Container string
	SessionID string
	// Name is the Event's name: hook_event_name of the payload.
	Name string
	CWD  string
	// Payload is what the Agent sent, as one line of JSON, with its secrets
	// masked.
	Payload []byte
	// Masked is set when a secret was masked in Payload, which is then not
	// the bytes the Agent sent.
	Masked bool
	// Source says who wrote the Event: empty for the Agent's hook (the only
	// source until `baton session end`), SourceBaton for Baton itself. An
	// Event of Baton is not Agent data: it names no process, and carries no
	// tool call.
	Source string
}

// SourceBaton marks an Event that Baton wrote on its own account, not one that
// an Agent reported.
const SourceBaton = "baton"

// FromAgent reports whether the Agent reported the Event, as opposed to Baton
// having written it.
func (e Event) FromAgent() bool { return e.Source == "" }

// Secrets is the source of the literal secrets that are masked in a payload.
type Secrets interface {
	// QuerySecretValues returns the values of the user's secrets. An error
	// (nothing to read, a file others can read, a malformed one) means that
	// the hook masks by pattern only.
	QuerySecretValues(ctx context.Context) ([]string, error)
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
	// SaveEventIn appends the Event to the file ref names, wherever that
	// is (a Session in a container has its file in its Project's directory),
	// under the same lock on the file as SaveEvent.
	SaveEventIn(ctx context.Context, ref SessionRef, e Event) error
	// QuerySessions lists the Event files.
	QuerySessions(ctx context.Context) ([]SessionRef, error)
	// QueryFirstEvent returns the oldest Event of the Session; ok is false
	// when the file holds none (an empty file, or lines no one can read).
	QueryFirstEvent(ctx context.Context, ref SessionRef) (e Event, ok bool, err error)
	// QueryEventsBackward calls yield with the Session's Events, newest first,
	// until it returns false. Lines that cannot be read are skipped.
	QueryEventsBackward(ctx context.Context, ref SessionRef, yield func(Event) bool) error
	// QueryPath returns the path of the Session's Event file on the host.
	QueryPath(ref SessionRef) string
	// QuerySize returns the size in bytes of the Session's Event file.
	QuerySize(ctx context.Context, ref SessionRef) (int64, error)
	// DeleteSession deletes the Session's Event file, and only that, under the
	// same lock on the file as SaveEvent. confirm runs with the lock held and
	// the file is deleted only if it returns true, so that a decision made
	// before the lock can be made again after any append that was ahead.
	// deleted is false when confirm said no or the file was already gone.
	DeleteSession(ctx context.Context, ref SessionRef, confirm func() (bool, error)) (size int64, deleted bool, err error)
	// SaveLaunch writes the launch record of its Project and Agent, replacing
	// the one before it.
	SaveLaunch(ctx context.Context, l Launch) error
	// QueryLaunches returns every launch record. A record that cannot be
	// read is skipped.
	QueryLaunches(ctx context.Context) ([]Launch, error)
	// DeleteLaunch deletes the record of the launch's Project and Agent if it
	// is still the record of that launch (same time), so that a newer launch
	// is never deleted. deleted is false when it was not.
	DeleteLaunch(ctx context.Context, l Launch) (deleted bool, err error)
	// ScrubSession replaces the payload of every line of the Session's Event
	// file that is an Event and for which mask says it changed, and marks the
	// line as masked; any other line, and every other field, stay as they are.
	// It takes the same lock as SaveEvent, writes the file anew only if a line
	// changed, and keeps its permission bits. It returns the number of lines
	// changed. With dryRun nothing is written.
	ScrubSession(ctx context.Context, ref SessionRef, mask func(payload []byte) ([]byte, bool), dryRun bool) (lines int, err error)
}

// Texts is the storage of the texts that were made from Events and by hand and
// may hold a secret: the Handoffs.
type Texts interface {
	// ScrubTexts replaces every text with mask(text) when that differs, in
	// place, atomically and with the same permission bits. It returns the texts
	// it changed (or, with dryRun, would change), and a text it could not
	// rewrite with its Err; the others are still done.
	ScrubTexts(ctx context.Context, mask func(text string) string, dryRun bool) ([]Scrubbed, error)
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
	// QueryContainer returns the id of the container the process runs in,
	// or "" when it is not in one or the id is not known.
	QueryContainer(ctx context.Context) (string, error)
}

// Bus is the Session domain.
type Bus struct {
	storer   Storer
	machine  Machine
	runtimes map[runtime.Runtime]runtimebus.Runtime
	secrets  Secrets
	texts    Texts
	now      func() time.Time
}

// New creates the Session domain over the Event files and the machine.
func New(storer Storer, machine Machine) *Bus {
	return &Bus{storer: storer, machine: machine, now: time.Now}
}

// WithSecrets lets the hook mask the user's own secrets too. Without it a
// payload is masked by pattern only.
func (b *Bus) WithSecrets(secrets Secrets) *Bus {
	b.secrets = secrets
	return b
}

// WithTexts lets Scrub go over the Handoffs too.
func (b *Bus) WithTexts(texts Texts) *Bus {
	b.texts = texts
	return b
}

// WithRuntimes lets the Bus tell whether the Agent of a Session still runs, by
// asking the Runtime the Session's Events name. Without it (the hook has no use
// for it) no Session is ever lost, and a Runtime that is not in the map judges
// nothing.
func (b *Bus) WithRuntimes(runtimes map[runtime.Runtime]runtimebus.Runtime) *Bus {
	b.runtimes = runtimes
	return b
}
