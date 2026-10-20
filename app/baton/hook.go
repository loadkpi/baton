package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/loadkpi/baton/business/domain/runtimebus/stores/runtimesandbox"
	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionfile"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionproc"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/runtime"
)

// containerRootVar names the one environment variable `baton hook` reads: the
// directory that stands for the root of the container, "/" when it is not set.
// It exists so that tests, which cannot write to /baton, can put the mount of a
// sandbox container somewhere else. It is read only for --runtime sandbox, so
// it has no effect on a host Session, and nothing else reads it.
const containerRootVar = "BATON_CONTAINER_ROOT"

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

	storer := sessionfile.New(filepath.Join(home, ".baton"))
	if *runtimeName == runtime.Sandbox.String() {
		// In a container the Project's Baton directory is mounted. Without
		// the mount this is an ai-sandbox started without Baton (ADR 0001):
		// there is nobody to tell, and the Agent must not notice.
		projectDir := filepath.Join(os.Getenv(containerRootVar), runtimesandbox.ContainerProject)
		if info, err := os.Stat(projectDir); err != nil || !info.IsDir() {
			return exitOK
		}
		storer = sessionfile.NewMounted(projectDir)
	}

	h, err := toBusHook(agentName, *profileName, *runtimeName, os.Getppid(), c.stdin)
	if err != nil {
		return c.errorf("hook: %v", err)
	}

	bus := sessionbus.New(storer, sessionproc.New())
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
