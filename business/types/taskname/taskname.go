// Package taskname defines the name of a Task.
package taskname

import (
	"fmt"
	"regexp"
)

// pattern keeps a name safe to use in a Terminal's tab title, where "/" and
// ":" separate the parts (<project>/<task>:<agent>), and as a file name.
var pattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Name identifies a Task within its Project, for example "login-fix".
type Name struct {
	value string
}

// Parse validates a Task name.
func Parse(s string) (Name, error) {
	if !pattern.MatchString(s) {
		return Name{}, fmt.Errorf("invalid task name %q: use lowercase letters, digits, '.', '_' and '-', starting with a letter or digit", s)
	}
	return Name{value: s}, nil
}

// String returns the name as shown and as used in file names.
func (n Name) String() string {
	return n.value
}
