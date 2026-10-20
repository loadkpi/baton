package sessionbus

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/loadkpi/baton/business/domain/runtimebus"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/runtime"
	"github.com/loadkpi/baton/business/types/status"
)

// Session is one native Agent conversation, as its Events show it.
type Session struct {
	Agent   agent.Agent
	Profile profilename.Name
	Runtime runtime.Runtime
	// Project is the Project whose Baton directory holds the Session's
	// Events, for a Session in a container; the zero value for the host. It
	// says which container CWD is seen from.
	Project   projectname.Name
	SessionID string
	// Ref names the Session's Event file to the storage.
	Ref SessionRef
	// CWD is the directory of the Session's first Event, which decides its
	// Project. Later Events may come from other directories.
	CWD    string
	Status status.Status
	// Since is when the Event that decided the status was reported.
	Since time.Time
	// TurnStart is, for a Session that waits for input, when the turn that
	// ended in this wait began: the time of the UserPromptSubmit Event before
	// the Event that decided the status. It is the zero time when no turn is
	// known, for example for a Session that has only started.
	TurnStart time.Time
	// Started is when the Session's first Event was reported.
	Started time.Time
	// Last is when its newest Event, of any kind, was reported.
	Last time.Time
}

// Key identifies a Session: the Agent's own session id is unique per Agent, and
// a Session in a container is told apart by the Project whose directory holds
// its Events.
type Key struct {
	Project   projectname.Name
	Agent     agent.Agent
	SessionID string
}

// Key returns the identity of the Session.
func (s Session) Key() Key {
	return Key{Project: s.Project, Agent: s.Agent, SessionID: s.SessionID}
}

// Active reports whether the Session can still be doing something: it has not
// ended and its Agent has not been found gone.
func (s Session) Active() bool {
	return s.Status != status.Ended && s.Status != status.Lost
}

// List returns every Session, the most recently active first. The status is
// that of the last Event the status table knows, except that a Session which
// has not ended but whose Agent process or container is gone is `lost`. That is
// found out here, on every read, by asking the Runtime of the Session; nothing
// is written. What could not be found out (docker is not there, say) is not
// held against the Session: it keeps the status of its Events, and the warnings
// say why.
func (b *Bus) List(ctx context.Context) (sessions []Session, warnings []string, err error) {
	refs, err := b.storer.QuerySessions(ctx)
	if err != nil {
		return nil, nil, err
	}

	var procs []*runtimebus.Process
	sessions = make([]Session, 0, len(refs))
	for _, ref := range refs {
		first, ok, err := b.storer.QueryFirstEvent(ctx, ref)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}

		s := Session{
			Agent: first.Agent, Profile: first.Profile, Runtime: first.Runtime, Project: ref.Project,
			SessionID: first.SessionID, Ref: ref, CWD: first.CWD,
			Status: status.Unknown, Since: first.TS, Started: first.TS, Last: first.TS,
		}
		var proc *runtimebus.Process
		newest, decided := true, false
		err = b.storer.QueryEventsBackward(ctx, ref, func(e Event) bool {
			if newest {
				s.Last, newest = e.TS, false
				if proc = toBusProcess(e); proc != nil {
					proc.Project = ref.Project
				}
			}
			st, known := status.FromEvent(e.Agent, e.Name)
			if !decided {
				if !known {
					return true
				}
				s.Status, s.Since, decided = st, e.TS, true
				// Only a wait that follows a turn has a turn to look for.
				return st == status.WaitingInput && e.Name != status.SessionStart
			}
			// The turn began at the newest prompt before the wait; any other
			// wait or the end of an earlier Session means there was none.
			if e.Name == status.TurnStart {
				s.TurnStart = e.TS
				return false
			}
			return !known || st == status.Running || st == status.WaitingApproval
		})
		if err != nil {
			return nil, nil, err
		}
		sessions = append(sessions, s)
		procs = append(procs, proc)
	}

	warnings = b.markLost(ctx, sessions, procs)

	sort.SliceStable(sessions, func(i, j int) bool {
		if !sessions[i].Since.Equal(sessions[j].Since) {
			return sessions[i].Since.After(sessions[j].Since)
		}
		return sessions[i].SessionID < sessions[j].SessionID
	})
	return sessions, warnings, nil
}

// toBusProcess is the process the newest Event of a Session names, or nil when
// the Event does not name one that can be judged: no PID or start time was
// recorded, or the PID is only the hook's parent because the Agent was not
// found among its ancestors, which says nothing about the Agent.
func toBusProcess(e Event) *runtimebus.Process {
	if e.AgentPID <= 0 || e.AgentStart == 0 || e.AgentPIDFallback {
		return nil
	}
	return &runtimebus.Process{Host: e.Host, PID: e.AgentPID, Start: e.AgentStart, Container: e.Container, Seen: e.TS}
}

// markLost sets the status of the Sessions whose Agent is gone to lost. Each
// Runtime is asked once, about all its Sessions that have not ended. The
// warnings say what a Runtime could not find out.
func (b *Bus) markLost(ctx context.Context, sessions []Session, procs []*runtimebus.Process) (warnings []string) {
	byRuntime := make(map[runtime.Runtime][]int)
	for i, s := range sessions {
		if s.Status != status.Ended && procs[i] != nil {
			byRuntime[s.Runtime] = append(byRuntime[s.Runtime], i)
		}
	}
	order := make([]runtime.Runtime, 0, len(byRuntime))
	for rt := range byRuntime {
		order = append(order, rt)
	}
	sort.Slice(order, func(i, j int) bool { return order[i].String() < order[j].String() })

	for _, rt := range order {
		indexes := byRuntime[rt]
		r, ok := b.runtimes[rt]
		if !ok {
			continue
		}
		asked := make([]runtimebus.Process, len(indexes))
		for j, i := range indexes {
			asked[j] = *procs[i]
		}
		verdicts, err := r.Alive(ctx, asked)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("cannot tell whether %s sessions are still running, showing what their events say: %v", rt, err))
		}
		for j, i := range indexes {
			if j < len(verdicts) && verdicts[j] == runtimebus.ProcessGone {
				sessions[i].Status = status.Lost
			}
		}
	}
	return warnings
}
