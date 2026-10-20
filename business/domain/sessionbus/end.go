package sessionbus

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/loadkpi/baton/business/types/status"
)

// EndedByHand is the reason in the payload of the Event `baton session end`
// writes.
const EndedByHand = "ended by hand"

// ErrAlreadyEnded is what End answers for a Session whose Events already say
// it ended.
var ErrAlreadyEnded = errors.New("session has already ended")

// NotFoundError says that no Session has an id that starts with Prefix.
type NotFoundError struct {
	Prefix string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("no session has an id starting with %q (baton ls shows them)", e.Prefix)
}

// AmbiguousError says that several Sessions have an id that starts with
// Prefix.
type AmbiguousError struct {
	Prefix     string
	Candidates []Session
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%d sessions have an id starting with %q", len(e.Candidates), e.Prefix)
}

// RunningError says that Baton can see the Agent of a Session still running,
// so End refused to end it.
type RunningError struct {
	Session Session
}

func (e *RunningError) Error() string {
	s := e.Session
	return fmt.Sprintf("session %s is still running: its %s process %d is alive (%s runtime)", s.SessionID, s.Agent, s.PID, s.Runtime)
}

// Resolve returns the Session whose id is id, or else the only one whose id
// starts with it. It is what the SESSION column of `baton ls` lets the user
// type: that column shows a prefix of the id.
func Resolve(sessions []Session, id string) (Session, error) {
	var matches []Session
	for _, s := range sessions {
		switch {
		case s.SessionID == id:
			return s, nil // a whole id wins over longer ids that start with it
		case id != "" && strings.HasPrefix(s.SessionID, id):
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 0:
		return Session{}, &NotFoundError{Prefix: id}
	case 1:
		return matches[0], nil
	}
	return Session{}, &AmbiguousError{Prefix: id, Candidates: matches}
}

// EndCommand is the command that ends the Session by hand, for a message to
// suggest when CannotJudge says Baton cannot tell by itself.
func (s Session) EndCommand() string {
	return "baton session end " + s.SessionID
}

// End records that a Session is over, for the case Baton cannot find that out:
// its Agent died without a SessionEnd and nothing names a process Baton can
// judge. It appends one Event, marked as Baton's own, to the Session's file
// under the lock the hook takes. Afterwards the Session has the status `ended`
// and is not active.
//
// It refuses a Session whose Agent process the Runtime found running, with a
// *RunningError, unless force is set. Nothing here, or anywhere, kills a
// process.
func (b *Bus) End(ctx context.Context, s Session, force bool) error {
	if s.Status == status.Ended {
		return ErrAlreadyEnded
	}
	if s.Liveness == AgentRunning && !force {
		return &RunningError{Session: s}
	}
	return b.storer.SaveEventIn(ctx, s.Ref, Event{
		TS:        b.now().UTC(),
		Agent:     s.Agent,
		Profile:   s.Profile,
		Runtime:   s.Runtime,
		SessionID: s.SessionID,
		Name:      status.SessionEnd,
		CWD:       s.CWD,
		Payload:   []byte(`{"reason":"` + EndedByHand + `"}`),
		Source:    SourceBaton,
	})
}
