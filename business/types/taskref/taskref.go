// Package taskref defines how a user points at a Task: by name or by id.
package taskref

import (
	"github.com/loadkpi/baton/business/types/taskid"
	"github.com/loadkpi/baton/business/types/taskname"
)

// Ref is a Task name or a Task id, as typed. The zero value is "no Task".
type Ref struct {
	id   taskid.ID
	name taskname.Name
}

// Parse tells an id from a name by its shape, which no name can have.
func Parse(s string) (Ref, error) {
	if taskid.Looks(s) {
		id, err := taskid.Parse(s)
		return Ref{id: id}, err
	}
	name, err := taskname.Parse(s)
	return Ref{name: name}, err
}

// IsZero reports whether the Ref points at nothing.
func (r Ref) IsZero() bool { return r.id.String() == "" && r.name.String() == "" }

// ID returns the id when the Ref is one.
func (r Ref) ID() (taskid.ID, bool) { return r.id, r.id.String() != "" }

// Name returns the name when the Ref is one.
func (r Ref) Name() (taskname.Name, bool) { return r.name, r.name.String() != "" }

// String returns the Ref as typed.
func (r Ref) String() string {
	if id, ok := r.ID(); ok {
		return id.String()
	}
	return r.name.String()
}
