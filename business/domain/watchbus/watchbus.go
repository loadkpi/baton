// Package watchbus notices what happens to Sessions while it runs and tells the
// user about the things that need them: an Agent waiting for approval, a long
// turn that ended, a Session that was lost.
//
// It keeps no state on disk. It asks the Session domain for the statuses (the
// one place that derives them, `lost` included) and compares each answer with
// the previous one in memory (DESIGN.md, D6).
package watchbus

import (
	"context"
	"fmt"
	"time"

	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/domain/workbus"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/status"
)

// longTurn is how long a turn must take, from the prompt to the Stop, for the
// end of it to be worth a notification (DESIGN.md, D6).
const longTurn = 30 * time.Second

// Message is one notification: which status a Session reached, and which Task
// and Agent it belongs to. It carries nothing from the Events' payloads.
type Message struct {
	Status status.Status
	Agent  agent.Agent
	// Task is "<project>/<task>", or empty for a Session outside any git
	// repository.
	Task string
}

// Notifier shows a Message to the user.
type Notifier interface {
	Notify(ctx context.Context, m Message) error
}

// Bus is the watching domain.
type Bus struct {
	sessions *sessionbus.Bus
	work     *workbus.Bus
	notifier Notifier

	// seen is what the previous look found, per Session. It lives in memory
	// only.
	seen map[sessionbus.Key]state
}

// state is what a look found out about a Session. The time of the Event that
// decided the status is part of it, so that a second approval request of the
// same Session is a new thing to tell, although the status is the same.
type state struct {
	status status.Status
	since  time.Time
}

// New creates the domain over the Sessions, the Tasks they belong to and a
// way to notify.
func New(sessions *sessionbus.Bus, work *workbus.Bus, notifier Notifier) *Bus {
	return &Bus{sessions: sessions, work: work, notifier: notifier}
}

// Start reads the current statuses and remembers them, notifying nothing: what
// already happened is not news. It returns the number of Sessions it found and
// what the Runtimes could not find out.
func (b *Bus) Start(ctx context.Context) (n int, warnings []string, err error) {
	sessions, warnings, err := b.sessions.List(ctx)
	if err != nil {
		return 0, nil, err
	}
	b.seen = make(map[sessionbus.Key]state, len(sessions))
	for _, s := range sessions {
		b.seen[s.Key()] = toState(s)
	}
	return len(sessions), warnings, nil
}

// Look reads the statuses again and notifies about the Sessions that changed
// since the previous look. A failure to notify is returned in the warnings and
// does not stop the others.
func (b *Bus) Look(ctx context.Context) (warnings []string, err error) {
	sessions, warnings, err := b.sessions.List(ctx)
	if err != nil {
		return nil, err
	}

	next := make(map[sessionbus.Key]state, len(sessions))
	var due []sessionbus.Session
	for _, s := range sessions {
		cur := toState(s)
		prev, known := b.seen[s.Key()]
		if known && prev.status == status.Lost && cur.status != status.Lost && cur.since.Equal(prev.since) && len(warnings) > 0 {
			// A Runtime that cannot answer (docker is down for a moment)
			// shows the Session with the status of its Events again. That is
			// not news, and the Session must not be told lost a second time
			// when the Runtime answers again.
			cur = prev
		}
		next[s.Key()] = cur
		if known && prev == cur {
			continue
		}
		if worthTelling(s) {
			due = append(due, s)
		}
	}
	b.seen = next

	if len(due) == 0 {
		return warnings, nil
	}
	tasks, err := b.taskNames(ctx)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("cannot name the tasks of the sessions: %v", err))
	}
	for _, s := range due {
		m := Message{Status: s.Status, Agent: s.Agent, Task: tasks[s.Key()]}
		if err := b.notifier.Notify(ctx, m); err != nil {
			warnings = append(warnings, fmt.Sprintf("cannot notify: %v", err))
		}
	}
	return warnings, nil
}

func toState(s sessionbus.Session) state {
	return state{status: s.Status, since: s.Since}
}

// worthTelling applies the table of DESIGN.md, D6, to a Session that changed.
func worthTelling(s sessionbus.Session) bool {
	switch s.Status {
	case status.WaitingApproval, status.Lost:
		return true
	case status.WaitingInput:
		// You are watching anyway after a short turn. The turn is judged by
		// the times of the Events, not by the clock of this process, which
		// may have started looking long after.
		return !s.TurnStart.IsZero() && s.Since.Sub(s.TurnStart) > longTurn
	default:
		return false
	}
}

// taskNames finds the Task of every Session, as "<project>/<task>".
func (b *Bus) taskNames(ctx context.Context) (map[sessionbus.Key]string, error) {
	listing, err := b.work.List(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[sessionbus.Key]string, len(listing.Sessions))
	for _, p := range listing.Sessions {
		if p.InGit {
			names[p.Session.Key()] = p.Place.Project.Name.String() + "/" + p.Task.Label()
		}
	}
	return names, nil
}
