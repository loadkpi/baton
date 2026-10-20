// Package taskbus knows the Tasks of a Project and which Task is current in
// each Working copy.
//
// The journal of a Project (current.jsonl) says "from time t, the current Task
// of Working copy X is T". It is only ever appended to, so the Task of a
// Session is the one that was current at the Session's first Event, however
// late a reader looks at the Session (DESIGN.md, D1).
package taskbus

import (
	"context"
	"fmt"
	"time"

	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/taskid"
	"github.com/loadkpi/baton/business/types/taskname"
	"github.com/loadkpi/baton/business/types/taskref"
	"github.com/loadkpi/baton/business/types/taskstatus"
)

// Task is a unit of work that outlives Sessions and Agents.
type Task struct {
	ID taskid.ID
	// Name is the zero value while the Task is unnamed.
	Name    taskname.Name
	Status  taskstatus.Status
	Created time.Time
}

// Label is what people see of the Task: its name, or its id while it has none.
func (t Task) Label() string {
	if t.Name.String() != "" {
		return t.Name.String()
	}
	return t.ID.String()
}

// Switch is one line of the journal: from At on, Task is the current Task of
// the Working copy.
type Switch struct {
	At          time.Time
	WorkingCopy string
	Task        taskid.ID
}

// Storer is the storage of Task records and of the journal of a Project.
type Storer interface {
	// QueryTask returns the record of a Task; ok is false when there is none.
	QueryTask(ctx context.Context, project projectname.Name, id taskid.ID) (t Task, ok bool, err error)
	// QueryTasks returns every Task of the Project.
	QueryTasks(ctx context.Context, project projectname.Name) ([]Task, error)
	// SaveTask writes the record of a Task.
	SaveTask(ctx context.Context, project projectname.Name, t Task) error
	// QuerySwitches returns the journal in the order it was written. Lines
	// that cannot be read are skipped.
	QuerySwitches(ctx context.Context, project projectname.Name) ([]Switch, error)
	// SaveSwitch appends a line to the journal.
	SaveSwitch(ctx context.Context, project projectname.Name, s Switch) error
	// Lock takes the lock that serializes the changes to the Tasks and the
	// journal of the Project and returns the function that releases it.
	Lock(ctx context.Context, project projectname.Name) (unlock func(), err error)
}

// Bus is the Task domain.
type Bus struct {
	storer Storer
}

// New creates the Task domain over a storage.
func New(storer Storer) *Bus {
	return &Bus{storer: storer}
}

// Adopt returns the Task of a Session that first showed up at time at in the
// Working copy: the Task that was current there at that time. When there was
// none, an unnamed Task is made, dated by at, and becomes current from at on, so
// that no Session is left without a Task.
func (b *Bus) Adopt(ctx context.Context, project projectname.Name, workingCopy string, at time.Time) (Task, error) {
	if t, ok, err := b.currentAt(ctx, project, workingCopy, at); err != nil || ok {
		return t, err
	}

	// Two readers may adopt the same Session at once: the second one looks
	// again under the lock.
	unlock, err := b.storer.Lock(ctx, project)
	if err != nil {
		return Task{}, err
	}
	defer unlock()

	if t, ok, err := b.currentAt(ctx, project, workingCopy, at); err != nil || ok {
		return t, err
	}
	t, err := b.create(ctx, project, taskname.Name{}, at)
	if err != nil {
		return Task{}, err
	}
	if err := b.storer.SaveSwitch(ctx, project, Switch{At: at, WorkingCopy: workingCopy, Task: t.ID}); err != nil {
		return Task{}, err
	}
	return t, nil
}

// Current returns the Task that is current in the Working copy now: the one of
// the latest line of the journal for it, whatever that line's date. ok is false
// when there is none. Unlike Adopt it never makes one.
func (b *Bus) Current(ctx context.Context, project projectname.Name, workingCopy string) (t Task, ok bool, err error) {
	return b.currentAt(ctx, project, workingCopy, time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC))
}

// currentAt finds the Task current in the Working copy at time at: the last
// line of the journal for it that is not later than at. A line that was written
// late but dated early (see Adopt) counts at its date.
func (b *Bus) currentAt(ctx context.Context, project projectname.Name, workingCopy string, at time.Time) (Task, bool, error) {
	switches, err := b.storer.QuerySwitches(ctx, project)
	if err != nil {
		return Task{}, false, err
	}
	var found Switch
	var ok bool
	for _, s := range switches {
		if s.WorkingCopy == workingCopy && !s.At.After(at) && (!ok || !s.At.Before(found.At)) {
			found, ok = s, true
		}
	}
	if !ok {
		return Task{}, false, nil
	}
	t, has, err := b.storer.QueryTask(ctx, project, found.Task)
	if err != nil {
		return Task{}, false, err
	}
	if !has {
		return Task{}, false, fmt.Errorf("project %s: current.jsonl names task %s, which has no record", project, found.Task)
	}
	return t, true, nil
}

// create makes an open Task, with the next id of the day of at, and saves it.
// The caller holds the lock.
func (b *Bus) create(ctx context.Context, project projectname.Name, name taskname.Name, at time.Time) (Task, error) {
	existing, err := b.storer.QueryTasks(ctx, project)
	if err != nil {
		return Task{}, err
	}
	ids := make([]taskid.ID, 0, len(existing))
	for _, t := range existing {
		ids = append(ids, t.ID)
	}
	t := Task{ID: taskid.Next(at, ids), Name: name, Status: taskstatus.Open, Created: at}
	if err := b.storer.SaveTask(ctx, project, t); err != nil {
		return Task{}, err
	}
	return t, nil
}

// Select makes a Task the current one of the Working copy from now on and
// returns it. Without a ref the Task is a new unnamed one.
//
// guard runs first, under the lock that every change to the Tasks takes, and
// may veto the change by returning an error: nothing is written then. It is how
// the rule "the current Task of a Working copy cannot change while a Session is
// active there" stays atomic with the change itself.
func (b *Bus) Select(ctx context.Context, project projectname.Name, workingCopy string, ref taskref.Ref, now time.Time, guard func(context.Context) error) (Task, error) {
	unlock, err := b.storer.Lock(ctx, project)
	if err != nil {
		return Task{}, err
	}
	defer unlock()

	if err := guard(ctx); err != nil {
		return Task{}, err
	}
	t, err := b.resolve(ctx, project, ref, now)
	if err != nil {
		return Task{}, err
	}
	if err := b.storer.SaveSwitch(ctx, project, Switch{At: now, WorkingCopy: workingCopy, Task: t.ID}); err != nil {
		return Task{}, err
	}
	return t, nil
}

// resolve finds the Task a ref points at. A name that no Task has yet makes a
// Task; so does no ref at all, which makes an unnamed one. An id that no Task
// has is an error, because an id is never chosen. The caller holds the lock.
func (b *Bus) resolve(ctx context.Context, project projectname.Name, ref taskref.Ref, now time.Time) (Task, error) {
	if ref.IsZero() {
		return b.create(ctx, project, taskname.Name{}, now)
	}
	tasks, err := b.storer.QueryTasks(ctx, project)
	if err != nil {
		return Task{}, err
	}
	if t, ok := find(tasks, ref); ok {
		return t, nil
	}
	if name, isName := ref.Name(); isName {
		return b.create(ctx, project, name, now)
	}
	return Task{}, fmt.Errorf("project %s has no task %s", project, ref)
}

// find returns the Task a ref points at among tasks.
func find(tasks []Task, ref taskref.Ref) (Task, bool) {
	id, isID := ref.ID()
	name, _ := ref.Name()
	for _, t := range tasks {
		if (isID && t.ID == id) || (!isID && t.Name == name) {
			return t, true
		}
	}
	return Task{}, false
}

// Rename gives a Task a name, or another one, and returns the Task.
func (b *Bus) Rename(ctx context.Context, project projectname.Name, ref taskref.Ref, name taskname.Name) (Task, error) {
	return b.change(ctx, project, ref, func(tasks []Task, t *Task) error {
		for _, other := range tasks {
			if other.ID != t.ID && other.Name == name {
				return fmt.Errorf("project %s already has a task named %s", project, name)
			}
		}
		t.Name = name
		return nil
	})
}

// SetStatus records what a human decided about a Task and returns the Task.
func (b *Bus) SetStatus(ctx context.Context, project projectname.Name, ref taskref.Ref, status taskstatus.Status) (Task, error) {
	return b.change(ctx, project, ref, func(_ []Task, t *Task) error {
		t.Status = status
		return nil
	})
}

// change applies edit to the Task the ref points at and saves the result, under
// the lock. edit sees every Task of the Project.
func (b *Bus) change(ctx context.Context, project projectname.Name, ref taskref.Ref, edit func(tasks []Task, t *Task) error) (Task, error) {
	unlock, err := b.storer.Lock(ctx, project)
	if err != nil {
		return Task{}, err
	}
	defer unlock()

	tasks, err := b.storer.QueryTasks(ctx, project)
	if err != nil {
		return Task{}, err
	}
	t, ok := find(tasks, ref)
	if !ok {
		return Task{}, fmt.Errorf("project %s has no task %s", project, ref)
	}
	if err := edit(tasks, &t); err != nil {
		return Task{}, err
	}
	if err := b.storer.SaveTask(ctx, project, t); err != nil {
		return Task{}, err
	}
	return t, nil
}

// Lookup returns the Task a ref points at. Unlike Select it never makes one.
func (b *Bus) Lookup(ctx context.Context, project projectname.Name, ref taskref.Ref) (Task, error) {
	tasks, err := b.storer.QueryTasks(ctx, project)
	if err != nil {
		return Task{}, err
	}
	t, ok := find(tasks, ref)
	if !ok {
		return Task{}, fmt.Errorf("project %s has no task %s", project, ref)
	}
	return t, nil
}
