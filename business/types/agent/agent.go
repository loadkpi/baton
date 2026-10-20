// Package agent defines the Agents Baton can configure.
package agent

import "fmt"

// Agent is a vendor CLI that does the work.
type Agent struct {
	name string
}

// Claude is Claude Code.
var Claude = Agent{name: "claude"}

// Parse converts a configured name into an Agent.
func Parse(s string) (Agent, error) {
	switch s {
	case Claude.name:
		return Claude, nil
	default:
		return Agent{}, fmt.Errorf("unsupported agent %q (supported: %s)", s, Claude.name)
	}
}

// String returns the name as written in the config.
func (a Agent) String() string {
	return a.name
}
