// Package taskstatus defines what a human decided about a Task.
package taskstatus

import "fmt"

// Status is the status of a Task. It is stored; whether a Task is running or
// waiting is derived from its Sessions and never stored (DESIGN.md, D1).
type Status struct {
	name string
}

// The statuses of a Task.
var (
	Open    = Status{name: "open"}
	Done    = Status{name: "done"}
	Dropped = Status{name: "dropped"}
)

// Parse converts a name typed by a user or read from a Task record.
func Parse(s string) (Status, error) {
	for _, st := range []Status{Open, Done, Dropped} {
		if s == st.name {
			return st, nil
		}
	}
	return Status{}, fmt.Errorf("unknown task status %q (known: open, done, dropped)", s)
}

// String returns the status as stored and shown.
func (s Status) String() string { return s.name }
