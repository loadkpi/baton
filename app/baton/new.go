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
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/taskname"
)

const newUsage = "usage: baton new <project> [task] --agent <claude> [--profile <name>]"

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
	task, err := toBusTaskName(parsed.task)
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
	profile, err := cfg.SelectProfile(a, chosen)
	if err != nil {
		return c.errorf("%v", err)
	}

	project, err := findProject(ctx, home, parsed.project)
	if err != nil {
		return c.errorf("%v", err)
	}

	req := launchbus.Request{Home: home, Project: project, Task: task, Agent: a, Profile: profile}
	if err := launchbus.New(launchguake.New()).Start(ctx, req); err != nil {
		return c.errorf("%v", err)
	}
	return exitOK
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

// toBusTaskName parses the optional Task name; no name gives the zero value.
func toBusTaskName(s string) (taskname.Name, error) {
	if s == "" {
		return taskname.Name{}, nil
	}
	return taskname.Parse(s)
}

// toBusProfileChoice parses the optional --profile; no flag gives the zero
// value, which means "the default".
func toBusProfileChoice(s string) (profilename.Name, error) {
	if s == "" {
		return profilename.Name{}, nil
	}
	return profilename.Parse(s)
}

// findProject returns the Project a name or a directory stands for. A
// directory inside a git repository registers the repository as a Project when
// Baton does not know it yet; a bare name must be a Project Baton knows.
func findProject(ctx context.Context, home, ref string) (projectbus.Project, error) {
	projects := projectbus.New(projectfile.New(filepath.Join(home, ".baton")))
	if !isPath(ref) {
		return projects.Named(ctx, ref)
	}

	dir, err := filepath.Abs(ref)
	if err != nil {
		return projectbus.Project{}, err
	}
	p, ok, err := projects.Resolve(ctx, dir)
	if err != nil {
		return projectbus.Project{}, err
	}
	if !ok {
		return projectbus.Project{}, fmt.Errorf("%s is not inside a git repository", dir)
	}
	return p, nil
}

// isPath tells a directory from a Project name: a name has no separator.
func isPath(ref string) bool {
	return ref == "." || ref == ".." || strings.ContainsRune(ref, os.PathSeparator)
}
