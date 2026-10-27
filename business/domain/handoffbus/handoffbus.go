// Package handoffbus moves a Task from one Agent or Profile to another: it
// waits until the Task's Session is over, writes the Handoff (the automatic
// part, joined with the narrative part if one was saved) and starts the
// receiving Session through the same path as `baton new`.
//
// The three steps are separate calls (Prepare, Wait, Complete) because the
// command treats an interrupt differently in each: before and during the wait
// nothing has been written yet, after it the command must run to its end.
package handoffbus

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/launchbus"
	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/domain/runtimebus"
	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/domain/taskbus"
	"github.com/loadkpi/baton/business/domain/workbus"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/runtime"
	"github.com/loadkpi/baton/business/types/taskid"
	"github.com/loadkpi/baton/business/types/taskref"
	"github.com/loadkpi/baton/foundation/git"
)

// commandCount is how many of the last shell commands go into the Handoff.
const commandCount = 10

// Storer is the storage of Handoffs.
type Storer interface {
	// Lock serializes the writers of one Task's Handoffs.
	Lock(ctx context.Context, project projectname.Name, task taskid.ID) (unlock func(), err error)
	// QueryDraft returns the narrative part saved ahead of the Handoff;
	// ok is false when there is none.
	QueryDraft(ctx context.Context, project projectname.Name, task taskid.ID) (text string, ok bool, err error)
	// QueryNext returns the number the Task's next Handoff gets.
	QueryNext(ctx context.Context, project projectname.Name, task taskid.ID) (int, error)
	// QueryLatest returns the number and the content of the Task's latest
	// Handoff; ok is false when there is none.
	QueryLatest(ctx context.Context, project projectname.Name, task taskid.ID) (n int, data []byte, ok bool, err error)
	// SaveDraft replaces the draft atomically, readable by its owner only,
	// and returns its path.
	SaveDraft(ctx context.Context, project projectname.Name, task taskid.ID, data []byte) (path string, err error)
	// SaveHandoff writes the n-th Handoff of the Task atomically, readable
	// by its owner only, and returns its path on the host.
	SaveHandoff(ctx context.Context, project projectname.Name, task taskid.ID, n int, data []byte) (path string, err error)
	// DeleteDraft removes the draft; there being none is not an error.
	DeleteDraft(ctx context.Context, project projectname.Name, task taskid.ID) error
}

// Bus is the Handoff domain.
type Bus struct {
	storer   Storer
	work     *workbus.Bus
	projects *projectbus.Bus
	tasks    *taskbus.Bus
	sessions *sessionbus.Bus
	launch   *launchbus.Bus
	runtimes map[runtime.Runtime]runtimebus.Runtime
	interval time.Duration
	now      func() time.Time
}

// New creates the domain. The Sessions are read through work, which also
// registers the Projects they are in; the receiving Session is started by
// launch. interval is how often a wait looks at the Sessions again.
func New(storer Storer, work *workbus.Bus, projects *projectbus.Bus, tasks *taskbus.Bus, sessions *sessionbus.Bus, launch *launchbus.Bus, runtimes map[runtime.Runtime]runtimebus.Runtime, interval time.Duration) *Bus {
	return &Bus{storer: storer, work: work, projects: projects, tasks: tasks, sessions: sessions, launch: launch, runtimes: runtimes, interval: interval, now: time.Now}
}

// Request asks for a Handoff of a Task to an Agent.
type Request struct {
	// Home is the home directory of the user.
	Home   string
	Config configbus.Config
	// Project and Task say what is handed over.
	Project projectname.Name
	Task    taskref.Ref
	// Agent and Profile say who gets it. Profile is the zero value for the
	// default of the Agent (or, in a sandbox, the Project's own Profile).
	Agent   agent.Agent
	Profile profilename.Name
}

// Plan is a Handoff that was checked and is ready to wait for.
type Plan struct {
	req     Request
	place   projectbus.Place
	task    taskbus.Task
	profile configbus.Profile
	// toProfile is the name of the Profile the Task goes to, as the Handoff
	// shows it.
	toProfile string
}

// Prepare finds the Task and the Working copy it was last worked on in, and
// refuses what could never be carried out, before anything waits or is written:
// an unknown Task, a Task with no Session, an Agent that cannot run in the
// Project, a Profile that does not exist, a sandbox that cannot work in that
// Working copy. warnings are things the user should know that do not stop it.
func (b *Bus) Prepare(ctx context.Context, req Request) (p *Plan, warnings []string, err error) {
	// Reading registers the Projects that Sessions were started in by hand.
	listing, err := b.work.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	project, err := b.projects.Named(ctx, req.Project.String())
	if err != nil {
		return nil, nil, err
	}
	task, err := b.tasks.Lookup(ctx, project.Name, req.Task)
	if err != nil {
		return nil, nil, err
	}
	latest, ok := latestOf(listing.Sessions, project.Name, task)
	if !ok {
		return nil, nil, fmt.Errorf("task %s/%s has no Session to hand over", project.Name, task.Label())
	}

	profile, err := launchbus.SelectProfile(req.Config, project, req.Agent, req.Profile)
	if err != nil {
		return nil, nil, err
	}
	toProfile := profile.Name
	if project.Runtime == runtime.Sandbox {
		if toProfile, err = profilename.Sandbox(project.Name, req.Agent); err != nil {
			return nil, nil, err
		}
	}
	if err := b.launch.Check(launchbus.Request{Place: latest.Place, Agent: req.Agent, Profile: profile}); err != nil {
		return nil, nil, err
	}
	return &Plan{req: req, place: latest.Place, task: task, profile: profile, toProfile: toProfile.String()}, listing.Warnings, nil
}

// latestOf returns the most recently active Session of the Task. The sessions
// are in the order List returns them.
func latestOf(sessions []workbus.Placed, project projectname.Name, task taskbus.Task) (workbus.Placed, bool) {
	for _, s := range sessions {
		if s.InGit && s.Place.Project.Name == project && s.Task.ID == task.ID {
			return s, true
		}
	}
	return workbus.Placed{}, false
}

// Wait blocks until no Session is active in the Working copy: the one-active-
// Session rule holds during a Handoff (DESIGN.md, D5). A Session ends with its
// SessionEnd Event, or is found lost. announce is called once, with the
// Sessions it is going to wait for, when there are any. Nothing is written. It
// returns ctx's error when ctx is cancelled first.
func (b *Bus) Wait(ctx context.Context, p *Plan, announce func([]sessionbus.Session)) error {
	announced := false
	for {
		active, err := b.activeIn(ctx, p)
		if err != nil {
			return err
		}
		if len(active) == 0 {
			return nil
		}
		if !announced {
			announce(active)
			announced = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(b.interval):
		}
	}
}

// activeIn returns the Sessions that are active in the Plan's Working copy.
func (b *Bus) activeIn(ctx context.Context, p *Plan) ([]sessionbus.Session, error) {
	listing, err := b.work.List(ctx)
	if err != nil {
		return nil, err
	}
	var active []sessionbus.Session
	for _, s := range listing.Sessions {
		if s.InGit && s.Place.WorkingCopy == p.place.WorkingCopy && s.Session.Active() {
			active = append(active, s.Session)
		}
	}
	return active, nil
}

// Result is what Complete did.
type Result struct {
	// Path is the Handoff file on the host.
	Path string
}

// Complete takes the automatic part of the Handoff now, so that the git state
// and the commands are those of the end of the old Session, writes the Handoff
// (with the draft as its narrative part if there is one, which it then removes)
// and starts the receiving Session with a first message that points to it. If
// the start fails the Handoff stays; the error says so.
func (b *Bus) Complete(ctx context.Context, p *Plan) (Result, error) {
	project := p.place.Project
	listing, err := b.work.List(ctx)
	if err != nil {
		return Result{}, err
	}
	from, ok := latestOf(listing.Sessions, project.Name, p.task)
	if !ok {
		return Result{}, fmt.Errorf("task %s/%s has no Session to hand over", project.Name, p.task.Label())
	}
	commands, err := b.sessions.Commands(ctx, from.Session, commandCount, savesNarrative)
	if err != nil {
		return Result{}, err
	}
	snapshot, err := git.Take(ctx, p.place.WorkingCopy)
	if err != nil {
		return Result{}, err
	}

	path, narrative, err := b.save(ctx, p, document{
		Created:     b.now(),
		Project:     project.Name.String(),
		Task:        p.task.ID.String(),
		TaskName:    p.task.Name.String(),
		WorkingCopy: p.place.WorkingCopy,
		From:        from.Session,
		EventFile:   b.sessions.EventFile(from.Session),
		ToAgent:     p.req.Agent.String(),
		ToProfile:   p.toProfile,
		ToRuntime:   project.Runtime.String(),
		Git:         snapshot,
		Commands:    commands,
	})
	if err != nil {
		return Result{}, err
	}

	// The receiving Agent names the file as its own Runtime sees it.
	rt, ok := b.runtimes[project.Runtime]
	if !ok {
		return Result{Path: path}, fmt.Errorf("the Handoff is written to %s, but the %s Runtime cannot be asked for its path", path, project.Runtime)
	}
	seen, err := rt.ContainerPath(ctx, project.Name, path)
	if err != nil {
		return Result{Path: path}, fmt.Errorf("the Handoff is written to %s, but the receiving Session cannot see it: %w", path, err)
	}
	// By id: the name the user typed may be renamed while the old Session ends.
	ref, err := taskref.Parse(p.task.ID.String())
	if err != nil {
		return Result{Path: path}, err
	}
	err = b.launch.Start(ctx, launchbus.Request{
		Home:    p.req.Home,
		Place:   p.place,
		Task:    ref,
		Agent:   p.req.Agent,
		Profile: p.profile,
		Prompt:  firstMessage(seen, narrative),
	})
	if err != nil {
		return Result{Path: path}, fmt.Errorf("the Handoff is written to %s, but the receiving Session was not started: %w", path, err)
	}
	return Result{Path: path}, nil
}

// save numbers the Handoff, joins the automatic part with the draft, writes the
// file and removes the draft, all under the Task's lock. It also returns who
// wrote the narrative part.
func (b *Bus) save(ctx context.Context, p *Plan, d document) (string, Narrative, error) {
	project := p.place.Project.Name
	unlock, err := b.storer.Lock(ctx, project, p.task.ID)
	if err != nil {
		return "", "", err
	}
	defer unlock()

	draft, hasDraft, err := b.storer.QueryDraft(ctx, project, p.task.ID)
	if err != nil {
		return "", "", err
	}
	d.Narrative = NarrativeMissing
	if hasDraft && strings.TrimSpace(draft) != "" {
		d.Narrative, d.NarrativeText = NarrativeHandingOver, draft
	}
	if d.Number, err = b.storer.QueryNext(ctx, project, p.task.ID); err != nil {
		return "", "", err
	}
	path, err := b.storer.SaveHandoff(ctx, project, p.task.ID, d.Number, d.render())
	if err != nil {
		return "", "", err
	}
	if d.Narrative == NarrativeHandingOver {
		// The draft is in the file now; a later one is for the next Handoff.
		if err := b.storer.DeleteDraft(ctx, project, p.task.ID); err != nil {
			return path, d.Narrative, fmt.Errorf("the Handoff is written to %s, but removing its draft failed: %w", path, err)
		}
	}
	return path, d.Narrative, nil
}

// firstMessage is what the receiving Session is told. path is the Handoff as
// the receiving Runtime sees it. It is one line, because the Terminal types it
// into a shell. When nobody wrote the narrative part, the receiver is asked to.
func firstMessage(path string, narrative Narrative) string {
	msg := "continue the Task from Handoff " + path
	if narrative == NarrativeMissing {
		msg += ". Its narrative part is missing: before you start, write it from the Facts section, the Event file and the git history, and save it with the baton-handoff skill (it pipes the Markdown to baton handoff save)"
	}
	return msg
}
