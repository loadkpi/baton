// Package runtimehost is the Runtime of Agents that run on the host.
package runtimehost

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/runtimebus"
	"github.com/loadkpi/baton/business/domain/syncbus"
	"github.com/loadkpi/baton/business/domain/syncbus/stores/syncfile"
	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/runtime"
	"github.com/loadkpi/baton/foundation/procfs"
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
	if r.Prompt != "" {
		command = append(command, r.Prompt)
	}

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

// ContainerPath is the path itself: the Agent runs on the host.
func (*Runtime) ContainerPath(_ context.Context, _ projectname.Name, p string) (string, error) {
	return p, nil
}

// Alive checks /proc: a Process runs when its PID exists, is not a zombie or dead
// (state Z or X) and has the recorded start time (a reused PID has another). An Event from another machine, which
// is what a different host name means (another computer, or a container),
// says nothing about this machine's /proc and is not judged.
func (r *Runtime) Alive(_ context.Context, procs []runtimebus.Process) ([]runtimebus.Liveness, error) {
	out := make([]runtimebus.Liveness, len(procs))
	if len(procs) == 0 {
		return out, nil
	}
	here, err := os.Hostname()
	if err != nil {
		return out, fmt.Errorf("host name: %w", err)
	}
	for i, p := range procs {
		if p.Host != here {
			continue
		}
		found, err := procfs.Read(p.PID)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			out[i] = runtimebus.ProcessGone
		case err != nil:
			// Unreadable is not the same as gone.
		case found.Exited():
			// A zombie keeps its stat file until the parent reaps it.
			out[i] = runtimebus.ProcessGone
		case found.Start == p.Start:
			out[i] = runtimebus.ProcessAlive
		default:
			out[i] = runtimebus.ProcessGone
		}
	}
	return out, nil
}
