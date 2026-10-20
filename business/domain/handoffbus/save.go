package handoffbus

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/domain/taskbus"
	"github.com/loadkpi/baton/business/domain/workbus"
	"github.com/loadkpi/baton/business/types/projectname"
)

// Saver keeps the narrative part of a Handoff that a Session wrote (`baton
// handoff save`). It finds the Task from a directory.
type Saver struct {
	storer   Storer
	projects *projectbus.Bus
	tasks    *taskbus.Bus
	// work is nil inside a container, which cannot see the host's Sessions.
	work *workbus.Bus
}

// NewSaver creates the domain on the host. Reading the Sessions through work
// gives a Session that was started by hand its Project and Task, so that a
// Task exists to save for.
func NewSaver(storer Storer, projects *projectbus.Bus, tasks *taskbus.Bus, work *workbus.Bus) *Saver {
	return &Saver{storer: storer, projects: projects, tasks: tasks, work: work}
}

// NewMountedSaver creates the domain inside a sandbox container. The
// directories there are not the host's, so the Working copy is not found from
// the directory: it is the Project's main checkout, the only place a sandbox
// Session runs (DESIGN.md, D3), as the mounted Project record names it. The
// stores are those of the mounted Project directory.
func NewMountedSaver(storer Storer, projects *projectbus.Bus, tasks *taskbus.Bus) *Saver {
	return &Saver{storer: storer, projects: projects, tasks: tasks}
}

// Saved says what a save did.
type Saved struct {
	// Path is the file that was written.
	Path string
	// Filled is true when the narrative went into the Task's latest Handoff,
	// which had none; it is false when it went into the draft for the next one.
	Filled bool
	// Handoff is the number of the Handoff that was filled, when Filled.
	Handoff int
}

// Save stores text as the narrative part. If the latest Handoff of the Task of
// dir has none, it is written there (the receiving Session saves it, author
// `receiving`). Otherwise it replaces the draft, which the next `baton handoff`
// puts into its Handoff (author `handing-over`). Both happen under the lock of
// the Task and atomically.
func (s *Saver) Save(ctx context.Context, dir, text string) (Saved, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Saved{}, errors.New("the narrative is empty; write it, then pipe it to this command")
	}
	text += "\n"

	project, workingCopy, err := s.locate(ctx, dir)
	if err != nil {
		return Saved{}, err
	}
	task, ok, err := s.tasks.Current(ctx, project, workingCopy)
	if err != nil {
		return Saved{}, err
	}
	if !ok {
		return Saved{}, fmt.Errorf("no Task is current in %s; start one with `baton new`", workingCopy)
	}

	unlock, err := s.storer.Lock(ctx, project, task.ID)
	if err != nil {
		return Saved{}, err
	}
	defer unlock()

	n, latest, has, err := s.storer.QueryLatest(ctx, project, task.ID)
	if err != nil {
		return Saved{}, err
	}
	if has {
		filled, missing, err := withNarrative(latest, text)
		if err != nil {
			return Saved{}, fmt.Errorf("handoff %d of %s: %w", n, task.Label(), err)
		}
		if missing {
			path, err := s.storer.SaveHandoff(ctx, project, task.ID, n, filled)
			return Saved{Path: path, Filled: true, Handoff: n}, err
		}
	}
	path, err := s.storer.SaveDraft(ctx, project, task.ID, []byte(text))
	return Saved{Path: path}, err
}

// locate finds the Project and the Working copy the command works for.
func (s *Saver) locate(ctx context.Context, dir string) (projectname.Name, string, error) {
	if s.work == nil {
		projects, err := s.projects.All(ctx)
		if err != nil {
			return projectname.Name{}, "", err
		}
		if len(projects) != 1 {
			return projectname.Name{}, "", errors.New("the mounted Baton directory of the Project has no project.toml")
		}
		return projects[0].Name, projects[0].Path, nil
	}
	// Reading registers the Projects that Sessions were started in by hand
	// and gives those Sessions their Task.
	if _, err := s.work.List(ctx); err != nil {
		return projectname.Name{}, "", err
	}
	place, ok, err := s.projects.Resolve(ctx, dir)
	if err != nil {
		return projectname.Name{}, "", err
	}
	if !ok {
		return projectname.Name{}, "", fmt.Errorf("%s is not inside a git repository, so it has no Task", dir)
	}
	return place.Project.Name, place.WorkingCopy, nil
}
