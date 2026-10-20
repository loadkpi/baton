// Package runtime defines where an Agent runs.
package runtime

import "fmt"

// Runtime is where an Agent runs: on the host, in an ai-sandbox container or
// in a service of the Project's own Docker Compose setup.
type Runtime struct {
	name string
}

// The Runtimes of the glossary.
var (
	Host    = Runtime{name: "host"}
	Sandbox = Runtime{name: "sandbox"}
	Compose = Runtime{name: "compose"}
)

// Parse converts a name written in a hook definition or a Project record.
func Parse(s string) (Runtime, error) {
	for _, r := range []Runtime{Host, Sandbox, Compose} {
		if s == r.name {
			return r, nil
		}
	}
	return Runtime{}, fmt.Errorf("unknown runtime %q (known: host, sandbox, compose)", s)
}

// String returns the name as written in files and shown in output.
func (r Runtime) String() string { return r.name }
