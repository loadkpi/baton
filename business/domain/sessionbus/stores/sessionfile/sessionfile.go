// Package sessionfile is the file storage of the Session domain: one JSON
// Lines file of raw Events per Session.
package sessionfile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/runtime"
	"github.com/loadkpi/baton/foundation/jsonl"
)

// eventVersion is the schema version of an Event line.
const eventVersion = 1

// Store keeps the Event files of host Sessions in <baton dir>/sessions, and
// reads those of Sessions in containers, which are in the Baton directory of
// their Project: <baton dir>/projects/<project>/sessions.
type Store struct {
	// root is the Baton state directory, where readers look for Event files.
	// It is empty for the storage of a hook in a container, which only writes.
	root string
	// dir is where SaveEvent writes.
	dir string
}

// New creates the storage. batonDir is the Baton state directory (~/.baton).
func New(batonDir string) *Store {
	return &Store{root: batonDir, dir: filepath.Join(batonDir, "sessions")}
}

// NewMounted creates the storage of a hook inside a container, where the
// Project's Baton directory is mounted at projectDir: Events go into its
// sessions directory. It lists nothing.
func NewMounted(projectDir string) *Store {
	return &Store{dir: filepath.Join(projectDir, "sessions")}
}

// fileEvent is the JSON document of one Event line. Primitive types only.
type fileEvent struct {
	V                int             `json:"v"`
	TS               string          `json:"ts"`
	Agent            string          `json:"agent"`
	Profile          string          `json:"profile"`
	Runtime          string          `json:"runtime"`
	Host             string          `json:"host,omitempty"`
	AgentPID         int             `json:"agent_pid,omitempty"`
	AgentStart       uint64          `json:"agent_start,omitempty"`
	AgentPIDFallback bool            `json:"agent_pid_fallback,omitempty"`
	Container        string          `json:"container,omitempty"`
	SessionID        string          `json:"session_id"`
	Event            string          `json:"event"`
	CWD              string          `json:"cwd"`
	Payload          json.RawMessage `json:"payload"`
	// Masked is set, only then, when a secret was masked in the payload.
	Masked bool `json:"masked,omitempty"`
	// Source is set, only then, on an Event that Baton wrote itself.
	Source string `json:"source,omitempty"`
}

func toFileEvent(e sessionbus.Event) fileEvent {
	return fileEvent{
		V:                eventVersion,
		TS:               e.TS.Format(time.RFC3339Nano),
		Agent:            e.Agent.String(),
		Profile:          e.Profile.String(),
		Runtime:          e.Runtime.String(),
		Host:             e.Host,
		AgentPID:         e.AgentPID,
		AgentStart:       e.AgentStart,
		AgentPIDFallback: e.AgentPIDFallback,
		Container:        e.Container,
		SessionID:        e.SessionID,
		Event:            e.Name,
		CWD:              e.CWD,
		Payload:          e.Payload,
		Masked:           e.Masked,
		Source:           e.Source,
	}
}

// eventPath is the file SaveEvent appends the Events of a Session to.
func (s *Store) eventPath(agent, sessionID string) string {
	return filepath.Join(s.dir, agent+"-"+sessionID+".jsonl")
}

// refDir is the directory that holds the Event files of ref's kind.
func (s *Store) refDir(project projectname.Name) string {
	if project == (projectname.Name{}) {
		return filepath.Join(s.root, "sessions")
	}
	return filepath.Join(s.root, "projects", project.String(), "sessions")
}

func (s *Store) refPath(ref sessionbus.SessionRef) string {
	return filepath.Join(s.refDir(ref.Project), ref.File)
}

// SaveEvent appends the Event to <dir>/<agent>-<session id>.jsonl, mode 0600.
// The payload is written as it came (secrets masked, see sessionbus), apart
// from the white space between its tokens, which would break the one line per
// Event.
func (s *Store) SaveEvent(_ context.Context, e sessionbus.Event) error {
	return appendEvent(s.eventPath(e.Agent.String(), e.SessionID), e)
}

// SaveEventIn appends the Event to the file ref names, with the same lock and
// the same line as SaveEvent, so that it never interleaves with a hook's.
func (s *Store) SaveEventIn(_ context.Context, ref sessionbus.SessionRef, e sessionbus.Event) error {
	return appendEvent(s.refPath(ref), e)
}

func appendEvent(path string, e sessionbus.Event) error {
	var line bytes.Buffer
	enc := json.NewEncoder(&line)
	enc.SetEscapeHTML(false) // the payload stays as the Agent wrote it
	if err := enc.Encode(toFileEvent(e)); err != nil {
		return err
	}
	return jsonl.Append(path, bytes.TrimRight(line.Bytes(), "\n"), 0o600)
}

// QuerySessions lists the *.jsonl files of the host's sessions directory and of
// the sessions directory of every Project. A missing directory holds no
// Sessions.
func (s *Store) QuerySessions(_ context.Context) ([]sessionbus.SessionRef, error) {
	refs, err := s.queryDir(projectname.Name{})
	if err != nil {
		return nil, err
	}

	projects, err := os.ReadDir(filepath.Join(s.root, "projects"))
	if os.IsNotExist(err) {
		return refs, nil
	}
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		name, err := projectname.Parse(p.Name())
		if err != nil || !p.IsDir() {
			continue // the lock file, or something that is not a Project's directory
		}
		more, err := s.queryDir(name)
		if err != nil {
			return nil, err
		}
		refs = append(refs, more...)
	}
	return refs, nil
}

func (s *Store) queryDir(project projectname.Name) ([]sessionbus.SessionRef, error) {
	entries, err := os.ReadDir(s.refDir(project))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var refs []sessionbus.SessionRef
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".jsonl") {
			refs = append(refs, sessionbus.SessionRef{Project: project, File: e.Name()})
		}
	}
	return refs, nil
}

// QueryFirstEvent returns the first line of the file that is an Event.
func (s *Store) QueryFirstEvent(_ context.Context, ref sessionbus.SessionRef) (e sessionbus.Event, ok bool, err error) {
	err = jsonl.Forward(s.refPath(ref), func(line []byte) bool {
		var parsed error
		if e, parsed = toBusEvent(line); parsed != nil {
			return true // not an Event; the next line may be
		}
		ok = true
		return false
	})
	return e, ok, err
}

// QueryEventsBackward passes the lines of the file that are Events, newest
// first.
func (s *Store) QueryEventsBackward(_ context.Context, ref sessionbus.SessionRef, yield func(sessionbus.Event) bool) error {
	return jsonl.Backward(s.refPath(ref), func(line []byte) bool {
		e, err := toBusEvent(line)
		if err != nil {
			return true
		}
		return yield(e)
	})
}

// QueryPath returns the path of the file of the Session.
func (s *Store) QueryPath(ref sessionbus.SessionRef) string {
	return s.refPath(ref)
}

// QuerySize returns the size in bytes of the file of the Session.
func (s *Store) QuerySize(_ context.Context, ref sessionbus.SessionRef) (int64, error) {
	info, err := os.Stat(s.refPath(ref))
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// DeleteSession deletes the file of the Session under the lock SaveEvent takes
// on it, when confirm, which runs with the lock held, says to.
func (s *Store) DeleteSession(_ context.Context, ref sessionbus.SessionRef, confirm func() (bool, error)) (size int64, deleted bool, err error) {
	return jsonl.Remove(s.refPath(ref), confirm)
}

// toBusEvent parses one line.
func toBusEvent(line []byte) (sessionbus.Event, error) {
	var fe fileEvent
	if err := json.Unmarshal(line, &fe); err != nil {
		return sessionbus.Event{}, err
	}
	if fe.V != eventVersion {
		return sessionbus.Event{}, fmt.Errorf("unsupported Event version %d", fe.V)
	}
	ts, err := time.Parse(time.RFC3339Nano, fe.TS)
	if err != nil {
		return sessionbus.Event{}, err
	}
	a, err := agent.Parse(fe.Agent)
	if err != nil {
		return sessionbus.Event{}, err
	}
	p, err := profilename.Parse(fe.Profile)
	if err != nil {
		return sessionbus.Event{}, err
	}
	r, err := runtime.Parse(fe.Runtime)
	if err != nil {
		return sessionbus.Event{}, err
	}
	return sessionbus.Event{
		TS: ts, Agent: a, Profile: p, Runtime: r, Host: fe.Host,
		AgentPID: fe.AgentPID, AgentStart: fe.AgentStart, AgentPIDFallback: fe.AgentPIDFallback, Container: fe.Container,
		SessionID: fe.SessionID, Name: fe.Event, CWD: fe.CWD, Payload: fe.Payload, Masked: fe.Masked, Source: fe.Source,
	}, nil
}
