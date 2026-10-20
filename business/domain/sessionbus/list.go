package sessionbus

import (
	"context"
	"sort"
	"time"

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
	// CWD is the directory of the Session's first Event, which decides its
	// Project. Later Events may come from other directories.
	CWD    string
	Status status.Status
	// Since is when the Event that decided the status was reported.
	Since time.Time
	// Started is when the Session's first Event was reported.
	Started time.Time
	// Last is when its newest Event, of any kind, was reported.
	Last time.Time
}

// Active reports whether the Session can still be doing something: its last
// Event is not the end of the conversation. A Session that crashed without a
// SessionEnd stays active until lost detection arrives (ticket 11).
func (s Session) Active() bool {
	return s.Status != status.Ended
}

// List returns every Session with a status derived from its last Event that
// the status table knows, the most recently active first.
func (b *Bus) List(ctx context.Context) ([]Session, error) {
	refs, err := b.storer.QuerySessions(ctx)
	if err != nil {
		return nil, err
	}

	sessions := make([]Session, 0, len(refs))
	for _, ref := range refs {
		first, ok, err := b.storer.QueryFirstEvent(ctx, ref)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}

		s := Session{
			Agent: first.Agent, Profile: first.Profile, Runtime: first.Runtime, Project: ref.Project,
			SessionID: first.SessionID, CWD: first.CWD,
			Status: status.Unknown, Since: first.TS, Started: first.TS, Last: first.TS,
		}
		newest := true
		err = b.storer.QueryEventsBackward(ctx, ref, func(e Event) bool {
			if newest {
				s.Last, newest = e.TS, false
			}
			st, known := status.FromEvent(e.Agent, e.Name)
			if known {
				s.Status, s.Since = st, e.TS
			}
			return !known
		})
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}

	sort.SliceStable(sessions, func(i, j int) bool {
		if !sessions[i].Since.Equal(sessions[j].Since) {
			return sessions[i].Since.After(sessions[j].Since)
		}
		return sessions[i].SessionID < sessions[j].SessionID
	})
	return sessions, nil
}
