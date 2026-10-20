// Package taskid defines the identity of a Task.
package taskid

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// pattern is <date>-<n>: the day the Task was made (UTC) and its number among
// that day's Tasks of the Project.
var pattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-[1-9]\d*$`)

// ID identifies a Task within its Project, for example "2026-10-20-1". It is
// generated, never chosen, and never changes, so a Task can be named, and
// renamed, without breaking what refers to it.
type ID struct {
	value string
}

// Parse validates an ID read from a file or typed by a user.
func Parse(s string) (ID, error) {
	if !Looks(s) {
		return ID{}, fmt.Errorf("invalid task id %q: want <date>-<n>, such as 2026-10-20-1", s)
	}
	return ID{value: s}, nil
}

// Looks reports whether s has the shape of an ID. A Task name must not, so that
// a word typed by a user means one Task only.
func Looks(s string) bool {
	return pattern.MatchString(s)
}

// Next returns the first ID of the day of at (UTC) that none of the existing
// IDs has.
func Next(at time.Time, existing []ID) ID {
	prefix := at.UTC().Format(time.DateOnly) + "-"
	highest := 0
	for _, id := range existing {
		rest, ok := strings.CutPrefix(id.value, prefix)
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(rest); err == nil && n > highest {
			highest = n
		}
	}
	return ID{value: prefix + strconv.Itoa(highest+1)}
}

// String returns the ID as shown and as used in file names.
func (id ID) String() string { return id.value }
