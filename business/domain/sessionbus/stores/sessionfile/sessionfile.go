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
	"github.com/loadkpi/baton/business/types/runtime"
	"github.com/loadkpi/baton/foundation/jsonl"
)

// eventVersion is the schema version of an Event line.
const eventVersion = 1

// Store keeps the Event files of host Sessions in <baton dir>/sessions.
type Store struct {
	dir string
}

// New creates the storage. batonDir is the Baton state directory (~/.baton).
func New(batonDir string) *Store {
	return &Store{dir: filepath.Join(batonDir, "sessions")}
}

// fileEvent is the JSON document of one Event line. Primitive types only.
type fileEvent struct {
	V                int             `json:"v"`
	TS               string          `json:"ts"`
	Agent            string          `json:"agent"`
	Profile          string          `json:"profile"`
	Runtime          string          `json:"runtime"`
	Host             string          `json:"host"`
	AgentPID         int             `json:"agent_pid"`
	AgentStart       uint64          `json:"agent_start"`
	AgentPIDFallback bool            `json:"agent_pid_fallback,omitempty"`
	SessionID        string          `json:"session_id"`
	Event            string          `json:"event"`
	CWD              string          `json:"cwd"`
	Payload          json.RawMessage `json:"payload"`
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
		SessionID:        e.SessionID,
		Event:            e.Name,
		CWD:              e.CWD,
		Payload:          e.Payload,
	}
}

func (s *Store) path(agent, sessionID string) string {
	return filepath.Join(s.dir, agent+"-"+sessionID+".jsonl")
}

// SaveEvent appends the Event to <dir>/<agent>-<session id>.jsonl, mode 0600.
// The raw payload is written as it came, apart from the white space between
// its tokens, which would break the one line per Event.
func (s *Store) SaveEvent(_ context.Context, e sessionbus.Event) error {
	var line bytes.Buffer
	enc := json.NewEncoder(&line)
	enc.SetEscapeHTML(false) // the payload stays as the Agent wrote it
	if err := enc.Encode(toFileEvent(e)); err != nil {
		return err
	}
	return jsonl.Append(s.path(e.Agent.String(), e.SessionID), bytes.TrimRight(line.Bytes(), "\n"), 0o600)
}

// QuerySessions lists the *.jsonl files of the directory. A missing directory
// holds no Sessions.
func (s *Store) QuerySessions(_ context.Context) ([]sessionbus.SessionRef, error) {
	entries, err := os.ReadDir(s.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var refs []sessionbus.SessionRef
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".jsonl") {
			refs = append(refs, sessionbus.SessionRef(e.Name()))
		}
	}
	return refs, nil
}

// QueryFirstEvent returns the first line of the file that is an Event.
func (s *Store) QueryFirstEvent(_ context.Context, ref sessionbus.SessionRef) (e sessionbus.Event, ok bool, err error) {
	err = jsonl.Forward(filepath.Join(s.dir, string(ref)), func(line []byte) bool {
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
	return jsonl.Backward(filepath.Join(s.dir, string(ref)), func(line []byte) bool {
		e, err := toBusEvent(line)
		if err != nil {
			return true
		}
		return yield(e)
	})
}

// toBusEvent parses one line. The payload is not needed to list Sessions and is
// left alone.
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
		AgentPID: fe.AgentPID, AgentStart: fe.AgentStart, AgentPIDFallback: fe.AgentPIDFallback,
		SessionID: fe.SessionID, Name: fe.Event, CWD: fe.CWD,
	}, nil
}
