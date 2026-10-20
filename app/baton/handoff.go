package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/configbus/stores/configfile"
	"github.com/loadkpi/baton/business/domain/handoffbus"
	"github.com/loadkpi/baton/business/domain/handoffbus/stores/handofffile"
	"github.com/loadkpi/baton/business/domain/launchbus"
	"github.com/loadkpi/baton/business/domain/launchbus/stores/launchguake"
	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionfile"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionproc"
	"github.com/loadkpi/baton/business/domain/taskbus"
	"github.com/loadkpi/baton/business/domain/taskbus/stores/taskfile"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/projectname"
)

const handoffUsage = "usage: baton handoff <project>/<task> --to <claude|codex>[@<profile>]"

// handoffInterval is how often `baton handoff` looks at the Sessions while it
// waits for the old one to end. A look reads the Event files and checks the
// recorded processes.
const handoffInterval = 500 * time.Millisecond

// exitInterrupted is the exit code of a command that was stopped by Ctrl-C
// (128 + SIGINT), the shell's convention.
const exitInterrupted = 130

// handoffArgs are the words of `baton handoff`, as typed.
type handoffArgs struct {
	task string
	to   string
}

// runHandoff implements `baton handoff <project>/<task> --to <agent>[@<profile>]`:
// it waits for the Task's active Session to end, writes the Handoff and opens a
// Session of the other Agent or Profile that continues from it.
func runHandoff(ctx context.Context, c console, home string, args []string) int {
	if len(args) > 0 && args[0] == "save" {
		return runHandoffSave(ctx, c, home, args[1:])
	}
	parsed, err := parseHandoffArgs(args)
	if err != nil {
		return c.usagef("handoff: %v (%s)", err, handoffUsage)
	}
	req, err := toBusHandoffRequest(home, parsed)
	if err != nil {
		return c.usagef("handoff: %v (%s)", err, handoffUsage)
	}
	if req.Config, err = configbus.New(configfile.New(configPath(home), home)).Load(ctx); err != nil {
		return c.errorf("%v", err)
	}

	bus := newHandoffBus(home, req.Config, c.stderr)

	// While the command waits, Ctrl-C (or a SIGTERM) ends it with nothing
	// written and nothing started. Past the wait the default is back: the
	// Handoff is written atomically, so a signal then leaves a whole file or none.
	waitCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	plan, warnings, err := bus.Prepare(waitCtx, req)
	for _, w := range warnings {
		c.warnf("%s", w)
	}
	if err != nil {
		return handoffFailed(c, waitCtx, err)
	}
	err = bus.Wait(waitCtx, plan, func(active []sessionbus.Session) {
		c.println(toAppWaiting(parsed.task, active))
	})
	if err != nil {
		return handoffFailed(c, waitCtx, err)
	}
	stop()

	res, err := bus.Complete(ctx, plan)
	if err != nil {
		return c.errorf("%v", err)
	}
	c.println("handoff " + res.Path)
	return exitOK
}

// handoffFailed reports a failure before the Handoff was written: an interrupt
// gets its own exit code and a message that says nothing was done.
func handoffFailed(c console, ctx context.Context, err error) int {
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		c.errorf("interrupted; no Handoff was written and no Session was started")
		return exitInterrupted
	}
	return c.errorf("%v", err)
}

// toAppWaiting is the line that says what the command is waiting for.
func toAppWaiting(task string, active []sessionbus.Session) string {
	s := active[0]
	line := fmt.Sprintf("waiting for the %s session %s of %s (profile %s, %s) to end; type /exit there, or end its process",
		s.Agent, s.SessionID, task, s.Profile, s.Status)
	if len(active) > 1 {
		line += fmt.Sprintf(" (%d more sessions are active in that working copy)", len(active)-1)
	}
	return line
}

// parseHandoffArgs splits the command line into the Task and the flag, which
// may stand before or after it.
func parseHandoffArgs(args []string) (handoffArgs, error) {
	var out handoffArgs
	fs := flag.NewFlagSet("handoff", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&out.to, "to", "", "")

	var positional []string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return handoffArgs{}, err
		}
		rest = fs.Args()
		if len(rest) > 0 {
			positional = append(positional, rest[0])
			rest = rest[1:]
		}
	}

	switch {
	case len(positional) == 0:
		return handoffArgs{}, errors.New("a task is required")
	case len(positional) > 1:
		return handoffArgs{}, fmt.Errorf("unexpected argument %q", positional[1])
	case out.to == "":
		return handoffArgs{}, errors.New("--to is required")
	}
	out.task = positional[0]
	return out, nil
}

// toBusHandoffRequest parses the words of the command line. The configuration
// is loaded by the caller.
func toBusHandoffRequest(home string, a handoffArgs) (handoffbus.Request, error) {
	project, ref, err := toBusTaskPath(a.task)
	if err != nil {
		return handoffbus.Request{}, err
	}
	to, profile, hasProfile := strings.Cut(a.to, "@")
	ag, err := agent.Parse(to)
	if err != nil {
		return handoffbus.Request{}, fmt.Errorf("--to: %w", err)
	}
	req := handoffbus.Request{Home: home, Task: ref, Agent: ag}
	if req.Project, err = projectname.Parse(project); err != nil {
		return handoffbus.Request{}, err
	}
	if hasProfile {
		if req.Profile, err = profilename.Parse(profile); err != nil {
			return handoffbus.Request{}, fmt.Errorf("--to: %w", err)
		}
	}
	return req, nil
}

// newHandoffBus wires the Handoff domain over the files under home. The
// receiving Session is started by the same domain as `baton new`.
func newHandoffBus(home string, cfg configbus.Config, log io.Writer) *handoffbus.Bus {
	batonDir := filepath.Join(home, ".baton")
	work := newWorkBus(home, cfg)
	sessions := sessionbus.New(sessionfile.New(batonDir), sessionproc.New()).WithRuntimes(runtimes(home, cfg))
	launch := launchbus.New(launchguake.New(), work, sessions, startable(home, cfg, log))
	return handoffbus.New(handofffile.New(batonDir), work, newProjects(home, cfg), taskbus.New(taskfile.New(batonDir)), sessions, launch, runtimes(home, cfg), handoffInterval)
}
