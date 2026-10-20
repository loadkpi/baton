package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/configbus/stores/configfile"
	"github.com/loadkpi/baton/business/domain/launchbus"
	"github.com/loadkpi/baton/business/domain/launchbus/stores/launchguake"
	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/domain/projectbus/stores/projectfile"
	"github.com/loadkpi/baton/business/domain/runtimebus"
	"github.com/loadkpi/baton/business/domain/runtimebus/stores/runtimehost"
	"github.com/loadkpi/baton/business/domain/runtimebus/stores/runtimesandbox"
	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionfile"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionproc"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/runtime"
	"github.com/loadkpi/baton/business/types/taskref"
)

const newUsage = "usage: baton new <project> [task] --agent <claude|codex> [--profile <name>]"

// newArgs are the words of `baton new`, as typed.
type newArgs struct {
	project string
	task    string
	agent   string
	profile string
}

// runNew implements `baton new`: it opens a Terminal tab that runs an Agent in
// a Project, registering the Project first if Baton does not know it yet.
func runNew(ctx context.Context, c console, home string, args []string) int {
	parsed, err := parseNewArgs(args)
	if err != nil {
		return c.usagef("new: %v (%s)", err, newUsage)
	}
	a, err := agent.Parse(parsed.agent)
	if err != nil {
		return c.usagef("new: --agent: %v", err)
	}
	task, err := toBusTaskRef(parsed.task)
	if err != nil {
		return c.usagef("new: %v", err)
	}
	chosen, err := toBusProfileChoice(parsed.profile)
	if err != nil {
		return c.usagef("new: --profile: %v", err)
	}

	configPath := filepath.Join(home, ".config", "baton", "config.toml")
	cfg, err := configbus.New(configfile.New(configPath, home)).Load(ctx)
	if err != nil {
		return c.errorf("%v", err)
	}
	place, err := findPlace(ctx, home, parsed.project)
	if err != nil {
		return c.errorf("%v", err)
	}

	// The Profile of a host Session is chosen from the config; the Profiles
	// inside a sandbox are the Project's own (<project>.<agent>) and are not.
	var profile configbus.Profile
	switch {
	case place.Project.Runtime == runtime.Host:
		if a == agent.Codex {
			return c.errorf("project %s runs in the %s Runtime; %s runs only in a sandbox (a Project with .ai-sandbox/run.sh)", place.Project.Name, place.Project.Runtime, a)
		}
		if profile, err = cfg.SelectProfile(a, chosen); err != nil {
			return c.errorf("%v", err)
		}
	case parsed.profile != "":
		return c.errorf("project %s runs in the %s Runtime, which has Profiles of its own; --profile applies to host Sessions only", place.Project.Name, place.Project.Runtime)
	}

	req := launchbus.Request{Home: home, Place: place, Task: task, Agent: a, Profile: profile}
	batonDir := filepath.Join(home, ".baton")
	sessions := sessionbus.New(sessionfile.New(batonDir), sessionproc.New())
	if err := launchbus.New(launchguake.New(), newWorkBus(batonDir), sessions, startable(batonDir, c.stderr)).Start(ctx, req); err != nil {
		return c.errorf("%v", err)
	}
	return exitOK
}

// startable are the Runtimes that can start an Agent. The sandbox says what it
// is doing (building an image can take minutes) on log.
func startable(batonDir string, log io.Writer) map[runtime.Runtime]runtimebus.Runtime {
	projects := projectbus.New(projectfile.New(batonDir))
	return map[runtime.Runtime]runtimebus.Runtime{
		runtime.Host:    runtimehost.New(),
		runtime.Sandbox: runtimesandbox.New(projects, batonDir).WithLog(log),
	}
}

// parseNewArgs splits the command line into the Project, the optional Task and
// the flags, which may come before, between or after them.
func parseNewArgs(args []string) (newArgs, error) {
	var out newArgs
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&out.agent, "agent", "", "")
	fs.StringVar(&out.profile, "profile", "", "")

	var positional []string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return newArgs{}, err
		}
		rest = fs.Args()
		if len(rest) > 0 {
			positional = append(positional, rest[0])
			rest = rest[1:]
		}
	}

	switch {
	case len(positional) == 0:
		return newArgs{}, errors.New("a project is required")
	case len(positional) > 2:
		return newArgs{}, fmt.Errorf("unexpected argument %q", positional[2])
	case out.agent == "":
		return newArgs{}, errors.New("--agent is required")
	}
	out.project = positional[0]
	if len(positional) == 2 {
		out.task = positional[1]
	}
	return out, nil
}

// toBusTaskRef parses the optional Task name or id; none gives the zero value.
func toBusTaskRef(s string) (taskref.Ref, error) {
	if s == "" {
		return taskref.Ref{}, nil
	}
	return taskref.Parse(s)
}

// toBusProfileChoice parses the optional --profile; no flag gives the zero
// value, which means "the default".
func toBusProfileChoice(s string) (profilename.Name, error) {
	if s == "" {
		return profilename.Name{}, nil
	}
	return profilename.Parse(s)
}

// findPlace returns the Working copy a name or a directory stands for. A bare
// name is a Project Baton knows, and means its main checkout. A directory
// inside a git repository means the Working copy that holds it, a git worktree
// included, and registers the repository as a Project when Baton does not know
// it yet.
func findPlace(ctx context.Context, home, ref string) (projectbus.Place, error) {
	projects := projectbus.New(projectfile.New(filepath.Join(home, ".baton")))
	if !isPath(ref) {
		p, err := projects.Named(ctx, ref)
		return projectbus.Place{Project: p, WorkingCopy: p.Path}, err
	}

	dir, err := filepath.Abs(ref)
	if err != nil {
		return projectbus.Place{}, err
	}
	place, ok, err := projects.Resolve(ctx, dir)
	if err != nil {
		return projectbus.Place{}, err
	}
	if !ok {
		return projectbus.Place{}, fmt.Errorf("%s is not inside a git repository", dir)
	}
	return place, nil
}

// isPath tells a directory from a Project name: a name has no separator.
func isPath(ref string) bool {
	return ref == "." || ref == ".." || strings.ContainsRune(ref, os.PathSeparator)
}
