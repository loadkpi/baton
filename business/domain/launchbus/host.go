package launchbus

import (
	"path/filepath"

	"github.com/loadkpi/baton/business/domain/configbus"
)

// This file is the one place that knows how an Agent is started on the host.
// Ticket 08 moves it behind the Runtime interface.

// apiKeyVars are removed from the environment of an Agent started on the
// host, so that a subscription login is never replaced by API billing by
// accident (spec story 30).
var apiKeyVars = []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"}

// hostTab describes the tab of a Session on the host.
//
// The tab's shell is started by Guake's own server process, which does not
// inherit the environment of baton, so the command clears the variables itself
// with env(1) and sets the Profile's directory the same way.
func hostTab(r Request) Tab {
	command := []string{"env"}
	for _, name := range apiKeyVars {
		command = append(command, "-u", name)
	}
	if isDefaultDir(r.Home, r.Profile) {
		// Claude Code reads ~/.claude when the variable is unset; one that
		// Guake's server happens to have would send it somewhere else.
		command = append(command, "-u", "CLAUDE_CONFIG_DIR")
	} else {
		command = append(command, "CLAUDE_CONFIG_DIR="+r.Profile.Dir)
	}
	command = append(command, r.Agent.Binary())

	title := r.Project.Name.String()
	if r.Task.String() != "" {
		title += "/" + r.Task.String()
	}
	title += ":" + r.Agent.String()

	return Tab{Dir: r.Project.Path, Title: title, Command: command}
}

// isDefaultDir is true for the Profile at ~/.claude, which Claude Code uses
// when CLAUDE_CONFIG_DIR is not set.
func isDefaultDir(home string, p configbus.Profile) bool {
	return filepath.Clean(p.Dir) == filepath.Join(home, ".claude")
}
