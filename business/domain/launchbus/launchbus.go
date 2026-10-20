// Package launchbus starts new Sessions: it decides what to run for an Agent
// and hands it to a Terminal that opens a tab for it.
package launchbus

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/domain/runtimebus"
	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/domain/workbus"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/runtime"
	"github.com/loadkpi/baton/business/types/taskref"
)

// Terminal is where a Session's Agent runs on screen. Guake is the only
// implementation in v1; tmux can plug in later (DESIGN.md, D8).
type Terminal interface {
	OpenTab(ctx context.Context, tab runtimebus.Tab) error
}

// Request asks for a new Session.
type Request struct {
	// Home is the home directory of the user.
	Home string
	// Place is the Working copy to start in.
	Place projectbus.Place
	// Task is the Task to continue or start; the zero value starts a new
	// unnamed one.
	Task    taskref.Ref
	Agent   agent.Agent
	Profile configbus.Profile
	// Prompt is the first message of the Session; empty for none.
	Prompt string
}

// Bus is the domain of starting Sessions.
type Bus struct {
	terminal Terminal
	work     *workbus.Bus
	sessions *sessionbus.Bus
	runtimes map[runtime.Runtime]runtimebus.Runtime
}

// New creates the domain over a Terminal, the domain that decides the Task, the
// Sessions (where launches are remembered) and the Runtimes that can start an
// Agent. A Project whose Runtime is not among them is refused.
func New(terminal Terminal, work *workbus.Bus, sessions *sessionbus.Bus, runtimes map[runtime.Runtime]runtimebus.Runtime) *Bus {
	return &Bus{terminal: terminal, work: work, sessions: sessions, runtimes: runtimes}
}

// Check refuses a request that Start could never carry out, without changing
// anything: the Project's Runtime cannot be started yet, or its Sessions may not
// run in this Working copy. Start checks first thing, and a caller that does
// work before it (a Handoff waits, then writes) asks earlier.
func (b *Bus) Check(r Request) error {
	project := r.Place.Project
	if _, ok := b.runtimes[project.Runtime]; !ok {
		return fmt.Errorf("project %s runs in the %s Runtime, which baton cannot start yet", project.Name, project.Runtime)
	}
	// A git worktree stores the host path of its git directory, which does not
	// exist inside a container, and has no sandbox of its own (DESIGN.md, D3).
	if project.Runtime != runtime.Host && r.Place.WorkingCopy != project.Path {
		return fmt.Errorf("project %s runs in the %s Runtime, whose Sessions run only in the main checkout %s, not in the worktree %s",
			project.Name, project.Runtime, project.Path, r.Place.WorkingCopy)
	}
	return nil
}

// SelectProfile returns the Profile a Session of the Agent starts under in the
// Project, from the user's choice (the zero value for none): a host Profile on
// the host, one of the Project's Profiles in its Compose service, and the zero
// Profile for a sandbox, whose Profiles are its own. It refuses what cannot
// run: Codex on the host or in Compose, and a choice of Profile in a sandbox.
func SelectProfile(cfg configbus.Config, project projectbus.Project, a agent.Agent, chosen profilename.Name) (configbus.Profile, error) {
	switch {
	case project.Runtime == runtime.Compose:
		return selectComposeProfile(cfg, project, a, chosen)
	case project.Runtime == runtime.Host:
		if a == agent.Codex {
			return configbus.Profile{}, fmt.Errorf("project %s runs in the %s Runtime; %s runs only in a sandbox (a Project with .ai-sandbox/run.sh)", project.Name, project.Runtime, a)
		}
		return cfg.SelectProfile(a, chosen)
	case chosen != (profilename.Name{}):
		return configbus.Profile{}, fmt.Errorf("project %s runs in the %s Runtime, which has Profiles of its own; --profile applies to host Sessions only", project.Name, project.Runtime)
	}
	return configbus.Profile{}, nil
}

// selectComposeProfile picks among the Claude Profiles inside the Project's
// service: the chosen one, else the only one. The config's default_profile is
// a host Profile and does not apply.
func selectComposeProfile(cfg configbus.Config, project projectbus.Project, a agent.Agent, chosen profilename.Name) (configbus.Profile, error) {
	if a != agent.Claude {
		return configbus.Profile{}, fmt.Errorf("project %s runs in the %s Runtime; %s does not run in a compose container yet (ticket 19), only claude does", project.Name, project.Runtime, a)
	}
	settings := cfg.ComposeOf(project.Name)
	if settings == nil {
		return configbus.Profile{}, fmt.Errorf("project %s runs in the %s Runtime but the config has no [projects.%s.compose] table", project.Name, project.Runtime, project.Name)
	}
	var candidates []configbus.Profile
	for _, p := range settings.Profiles {
		if p.Agent == a {
			candidates = append(candidates, p)
		}
	}
	var names []string
	for _, p := range candidates {
		names = append(names, p.Name.String())
	}
	switch {
	case chosen != (profilename.Name{}):
		for _, p := range candidates {
			if p.Name == chosen {
				return p, nil
			}
		}
		return configbus.Profile{}, fmt.Errorf("unknown profile %q (%s profiles in the %s service of project %s: %s)", chosen, a, project.Runtime, project.Name, strings.Join(names, ", "))
	case len(candidates) == 1:
		return candidates[0], nil
	case len(candidates) == 0:
		return configbus.Profile{}, fmt.Errorf("project %s has no %s profile in its compose service", project.Name, a)
	}
	return configbus.Profile{}, fmt.Errorf("several %s profiles in the compose service of project %s (%s); choose one with --profile", a, project.Name, strings.Join(names, ", "))
}

// Start makes the Task of the request the current one of the Working copy and
// opens a tab that runs the Agent in it. Nothing is changed when the Project's
// Runtime cannot be started yet, when its Sessions may not run in this Working
// copy or when the Working copy has an active Session.
func (b *Bus) Start(ctx context.Context, r Request) error {
	if err := b.Check(r); err != nil {
		return err
	}
	project := r.Place.Project
	rt := b.runtimes[project.Runtime]
	task, err := b.work.Begin(ctx, r.Place, r.Task)
	if err != nil {
		return err
	}
	began := time.Now()
	tab, err := rt.Start(ctx, runtimebus.StartRequest{
		Home:        r.Home,
		Project:     project.Name,
		WorkingCopy: r.Place.WorkingCopy,
		Task:        task.Label(),
		Agent:       r.Agent,
		Profile:     r.Profile,
		Prompt:      r.Prompt,
	})
	if err != nil {
		return err
	}
	if err := b.terminal.OpenTab(ctx, tab); err != nil {
		return fmt.Errorf("open a tab for %s: %w", tab.Title, err)
	}
	// Codex runs Baton's hooks only after the user trusted them, so a Codex
	// that Baton started can stay silent for good. Remembering the launch is
	// how `baton ls` can tell. Claude needs no such thing: its hooks run once
	// sync wrote them.
	if r.Agent == agent.Codex {
		return b.recordLaunch(ctx, began, project.Name, project.Runtime, task.Label(), tab.Title, r.Agent)
	}
	return nil
}

func (b *Bus) recordLaunch(ctx context.Context, at time.Time, project projectname.Name, rt runtime.Runtime, task, title string, a agent.Agent) error {
	profile, err := profilename.Sandbox(project, a)
	if err != nil {
		return fmt.Errorf("name the %s Profile of project %s: %w", a, project, err)
	}
	l := sessionbus.Launch{TS: at, Project: project, Agent: a, Profile: profile, Runtime: rt, Task: task, Title: title}
	if err := b.sessions.RecordLaunch(ctx, l); err != nil {
		return fmt.Errorf("the tab %s is open, but remembering the launch failed: %w", title, err)
	}
	return nil
}
