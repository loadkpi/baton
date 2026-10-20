// Package projectname defines the name of a Project.
package projectname

import (
	"fmt"
	"regexp"
)

// pattern keeps a name safe to use as a directory name: a Project's records
// live under ~/.baton/projects/<name>.
var pattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Name identifies a Project, for example "my-app".
type Name struct {
	value string
}

// Parse validates a Project name.
func Parse(s string) (Name, error) {
	if !pattern.MatchString(s) {
		return Name{}, fmt.Errorf("invalid project name %q: use lowercase letters, digits, '.', '_' and '-', starting with a letter or digit", s)
	}
	return Name{value: s}, nil
}

// String returns the name as shown and as used in file names.
func (n Name) String() string {
	return n.value
}
