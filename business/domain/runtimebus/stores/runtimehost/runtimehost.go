// Package runtimehost is the Runtime of Agents that run on the host.
package runtimehost

import (
	"context"
	"path/filepath"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/runtimebus"
	"github.com/loadkpi/baton/business/domain/syncbus"
	"github.com/loadkpi/baton/business/domain/syncbus/stores/syncfile"
	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/runtime"
)

// Runtime is the host.
type Runtime struct{}

// New creates the host Runtime.
func New() *Runtime { return &Runtime{} }

var _ runtimebus.Runtime = (*Runtime)(nil)

// Name is "host".
func (*Runtime) Name() runtime.Runtime { return runtime.Host }

// apiKeyVars are removed from the environment of an Agent started on the
// host, so that a subscription login is never replaced by API billing by
// accident (spec story 30).
var apiKeyVars = []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"}

// Start describes the tab of a Session on the host.
//
// The tab's shell is started by Guake's own server process, which does not
// inherit the environment of baton, so the command clears the variables itself
// with env(1) and sets the Profile's directory the same way.
func (*Runtime) Start(_ context.Context, r runtimebus.StartRequest) (runtimebus.Tab, error) {
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

	title := r.Project.String() + "/" + r.Task + ":" + r.Agent.String()
	return runtimebus.Tab{Dir: r.WorkingCopy, Title: title, Command: command}, nil
}

// isDefaultDir is true for the Profile at ~/.claude, which Claude Code uses
// when CLAUDE_CONFIG_DIR is not set.
func isDefaultDir(home string, p configbus.Profile) bool {
	return filepath.Clean(p.Dir) == filepath.Join(home, ".claude")
}

// Targets are the Profiles of the config. The hooks call the binary in
// ~/.baton/bin, the directory that is mounted into sandboxes.
func (*Runtime) Targets(_ context.Context, cfg configbus.Config) ([]syncbus.Target, []string, error) {
	targets := make([]syncbus.Target, 0, len(cfg.Profiles))
	for _, p := range cfg.Profiles {
		targets = append(targets, syncbus.Target{
			Profile:    p,
			Runtime:    runtime.Host,
			Home:       cfg.Home,
			HookBinary: filepath.Join(cfg.Home, ".baton", "bin", "baton"),
			Files:      syncfile.NewProfileDir(p.Dir, p.Dir),
		})
	}
	return targets, nil, nil
}

// HostPath is filled in when a ticket needs it.
func (*Runtime) HostPath(context.Context, projectname.Name, string) (string, error) {
	return "", runtimebus.ErrNotImplemented
}

// Alive is filled in by ticket 11.
func (*Runtime) Alive(context.Context, runtimebus.Process) (bool, error) {
	return false, runtimebus.ErrNotImplemented
}
