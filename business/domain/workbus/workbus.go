// Package workbus puts Sessions where they belong: in a Working copy of a
// Project, and in the Task that was current there.
//
// It joins three domains that know nothing of one another: Sessions know their
// directory, Projects know directories, and Tasks know Working copies.
package workbus

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/domain/runtimebus"
	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/domain/taskbus"
	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/runtime"
	"github.com/loadkpi/baton/business/types/taskref"
)

// Bus joins the Session, Project and Task domains.
type Bus struct {
	sessions *sessionbus.Bus
	projects *projectbus.Bus
	tasks    *taskbus.Bus
	runtimes map[runtime.Runtime]runtimebus.Runtime
	now      func() time.Time
}

// New creates the domain over the three it joins. The Runtimes map the
// directories of Sessions inside their containers to host paths.
func New(sessions *sessionbus.Bus, projects *projectbus.Bus, tasks *taskbus.Bus, runtimes map[runtime.Runtime]runtimebus.Runtime) *Bus {
	return &Bus{sessions: sessions, projects: projects, tasks: tasks, runtimes: runtimes, now: func() time.Time { return time.Now().UTC() }}
}

// Placed is a Session with the place and the Task it belongs to.
type Placed struct {
	Session sessionbus.Session
	// InGit is false for a Session outside any git repository, which has no
	// Project, Working copy or Task.
	InGit bool
	Place projectbus.Place
	Task  taskbus.Task
}

// Listing is what `baton ls` shows.
type Listing struct {
	// Sessions are the Sessions with Events, the most recently active first.
	Sessions []Placed
	// Silent are the Sessions Baton started that have reported no Event yet.
	Silent []sessionbus.Launch
	// Warnings are things the user should know that do not stop the listing:
	// a Runtime that could not tell whether its Sessions still run.
	Warnings []string
}

// List returns every Session, the most recently active first, with its place
// and Task, and the launches that no Event has followed. A Session's place is
// that of the directory of its first Event. Reading registers the Projects the
// Sessions are in, and gives a Session that has no Task an unnamed one.
func (b *Bus) List(ctx context.Context) (Listing, error) {
	sessions, warnings, err := b.sessions.List(ctx)
	if err != nil {
		return Listing{}, err
	}
	silent, err := b.sessions.Silent(ctx, sessions)
	if err != nil {
		return Listing{}, err
	}
	out, err := b.place(ctx, sessions)
	if err != nil {
		return Listing{}, err
	}

	// Sessions are adopted oldest first. A Session that starts later in a
	// Working copy where an earlier one has not been adopted yet must find
	// the Task that one gets, instead of making a Task of its own.
	order := make([]int, len(out))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, c int) bool {
		sa, sc := out[order[a]].Session, out[order[c]].Session
		if !sa.Started.Equal(sc.Started) {
			return sa.Started.Before(sc.Started)
		}
		return sa.SessionID < sc.SessionID
	})
	for _, i := range order {
		if !out[i].InGit {
			continue
		}
		p := &out[i]
		if p.Task, err = b.tasks.Adopt(ctx, p.Place.Project.Name, p.Place.WorkingCopy, p.Session.Started); err != nil {
			return Listing{}, err
		}
	}
	return Listing{Sessions: out, Silent: silent, Warnings: warnings}, nil
}

// place finds the Working copy of each Session. Each directory is looked at
// once.
func (b *Bus) place(ctx context.Context, sessions []sessionbus.Session) ([]Placed, error) {
	type key struct {
		project projectname.Name
		dir     string
	}
	type found struct {
		place projectbus.Place
		ok    bool
	}
	byDir := make(map[key]found)
	out := make([]Placed, 0, len(sessions))
	for _, s := range sessions {
		k := key{project: s.Project, dir: s.CWD}
		f, seen := byDir[k]
		if !seen {
			dir, ok := b.hostDir(ctx, s)
			f = found{}
			if ok {
				place, inGit, err := b.projects.Resolve(ctx, dir)
				if err != nil {
					return nil, err
				}
				f = found{place: place, ok: inGit}
			}
			byDir[k] = f
		}
		out = append(out, Placed{Session: s, InGit: f.ok, Place: f.place})
	}
	return out, nil
}

// hostDir is the directory of a Session's first Event as a path of the host. A
// Session on the host has it as it is. One in a container is mapped through the
// mounts of its Runtime, never assumed to see the same paths; when that cannot
// be done (the container was not started by Baton, or the directory is not
// under a mount) the Session has no place.
func (b *Bus) hostDir(ctx context.Context, s sessionbus.Session) (string, bool) {
	if s.Project == (projectname.Name{}) {
		return s.CWD, true
	}
	rt, ok := b.runtimes[s.Runtime]
	if !ok {
		return "", false
	}
	dir, err := rt.HostPath(ctx, s.Project, s.CWD)
	return dir, err == nil
}

// ActiveError says that a Working copy cannot take another Session, or a
// change of its current Task, because a Session is active there. It names the
// Session so that the user can judge whether it is really running.
type ActiveError struct {
	Place   projectbus.Place
	Session sessionbus.Session
}

func (e *ActiveError) Error() string {
	s := e.Session
	msg := fmt.Sprintf("working copy %s of project %s already has an active session: %s, profile %s, session %s, last event %s (%s); end it, or hand the Task over with baton handoff",
		e.Place.WorkingCopy, e.Place.Project.Name, s.Agent, s.Profile, s.SessionID, s.Last.UTC().Format(time.RFC3339Nano), s.Status)
	if s.CannotJudge() {
		msg += fmt.Sprintf("; baton cannot tell whether its Agent is still running, so if it is gone, run %s", s.EndCommand())
	}
	return msg
}

// Begin makes a Task the current one of a Working copy, for a Session that is
// about to start there: the Task the ref points at, or a new unnamed one when
// there is no ref. It refuses with an *ActiveError while a Session is active in
// the Working copy, and then changes nothing.
func (b *Bus) Begin(ctx context.Context, place projectbus.Place, ref taskref.Ref) (taskbus.Task, error) {
	guard := func(ctx context.Context) error {
		sessions, _, err := b.sessions.List(ctx)
		if err != nil {
			return err
		}
		placed, err := b.place(ctx, sessions)
		if err != nil {
			return err
		}
		for _, p := range placed { // most recently active first
			if p.InGit && p.Place.WorkingCopy == place.WorkingCopy && p.Session.Active() {
				return &ActiveError{Place: place, Session: p.Session}
			}
		}
		return nil
	}
	return b.tasks.Select(ctx, place.Project.Name, place.WorkingCopy, ref, b.now(), guard)
}
