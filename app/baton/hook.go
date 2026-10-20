package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionfile"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionproc"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/runtime"
)

// runHook implements `baton hook <agent> --profile <name> --runtime <runtime>`,
// which Agents call from their hooks. It reads the payload from standard input
// and appends the Event to the Session's file. It prints nothing on success:
// an Agent may read what a hook writes.
//
// Every failure exits with 1, never 2: for Claude Code an exit code of 2 from a
// hook blocks the action the hook is about.
func runHook(ctx context.Context, c console, home string, args []string) int {
	if len(args) == 0 {
		return c.errorf("hook: usage: baton hook <agent> --profile <name> --runtime <host|sandbox|compose>")
	}
	agentName, flagArgs := args[0], args[1:]

	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profileName := fs.String("profile", "", "name of the Profile")
	runtimeName := fs.String("runtime", "", "where the Agent runs")
	if err := fs.Parse(flagArgs); err != nil || fs.NArg() != 0 {
		return c.errorf("hook: usage: baton hook <agent> --profile <name> --runtime <host|sandbox|compose>")
	}

	h, err := toBusHook(agentName, *profileName, *runtimeName, os.Getppid(), c.stdin)
	if err != nil {
		return c.errorf("hook: %v", err)
	}

	bus := sessionbus.New(sessionfile.New(filepath.Join(home, ".baton")), sessionproc.New())
	if err := bus.Record(ctx, h); err != nil {
		return c.errorf("hook: %v", err)
	}
	return exitOK
}

// toBusHook parses what the command line and standard input hold.
func toBusHook(agentName, profileName, runtimeName string, parentPID int, stdin io.Reader) (sessionbus.Hook, error) {
	a, err := agent.Parse(agentName)
	if err != nil {
		return sessionbus.Hook{}, err
	}
	p, err := profilename.Parse(profileName)
	if err != nil {
		return sessionbus.Hook{}, fmt.Errorf("--profile: %w", err)
	}
	r, err := runtime.Parse(runtimeName)
	if err != nil {
		return sessionbus.Hook{}, fmt.Errorf("--runtime: %w", err)
	}
	payload, err := io.ReadAll(stdin)
	if err != nil {
		return sessionbus.Hook{}, fmt.Errorf("read payload: %w", err)
	}
	return sessionbus.Hook{Agent: a, Profile: p, Runtime: r, ParentPID: parentPID, Payload: payload}, nil
}
