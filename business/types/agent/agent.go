// Package agent defines the Agents Baton can configure.
package agent

import "fmt"

// Agent is a vendor CLI that does the work.
type Agent struct {
	name string
}

// Claude is Claude Code.
var Claude = Agent{name: "claude"}

// Codex is the Codex CLI. It runs only in a sandbox (DESIGN.md, D3); Parse
// accepts it, and the code that starts an Agent or reads a host Profile refuses
// it where it cannot run.
var Codex = Agent{name: "codex"}

// Parse converts a configured name into an Agent.
func Parse(s string) (Agent, error) {
	switch s {
	case Claude.name:
		return Claude, nil
	case Codex.name:
		return Codex, nil
	default:
		return Agent{}, fmt.Errorf("unsupported agent %q (supported: %s, %s)", s, Claude.name, Codex.name)
	}
}

// String returns the name as written in the config.
func (a Agent) String() string {
	return a.name
}

// Binary is the name of the Agent's executable, which is also the name the
// kernel keeps for its process.
func (a Agent) Binary() string {
	return a.name
}
