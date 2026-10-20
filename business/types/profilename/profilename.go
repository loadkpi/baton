// Package profilename defines the name of a Profile.
package profilename

import (
	"fmt"
	"regexp"

	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/projectname"
)

// pattern keeps a name safe to use as a file name: Baton stores per-Profile
// files (manifests, backups) under it.
var pattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Name identifies a Profile, for example "claude-personal".
type Name struct {
	value string
}

// Parse validates a Profile name.
func Parse(s string) (Name, error) {
	if !pattern.MatchString(s) {
		return Name{}, fmt.Errorf("invalid profile name %q: use lowercase letters, digits, '.', '_' and '-', starting with a letter or digit", s)
	}
	return Name{value: s}, nil
}

// String returns the name as written in the config.
func (n Name) String() string {
	return n.value
}

// Sandbox is the name of an Agent's Profile inside a Project's sandbox:
// <project>.<agent>.
func Sandbox(project projectname.Name, a agent.Agent) (Name, error) {
	return Parse(project.String() + "." + a.String())
}
