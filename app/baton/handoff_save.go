package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/loadkpi/baton/business/domain/handoffbus"
	"github.com/loadkpi/baton/business/domain/handoffbus/stores/handofffile"
	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/domain/projectbus/stores/projectfile"
	"github.com/loadkpi/baton/business/domain/runtimebus/stores/runtimesandbox"
	"github.com/loadkpi/baton/business/domain/taskbus"
	"github.com/loadkpi/baton/business/domain/taskbus/stores/taskfile"
)

const handoffSaveUsage = "usage: baton handoff save   (the narrative, in Markdown, on standard input)"

// runHandoffSave implements `baton handoff save`: it reads the narrative part
// of a Handoff from standard input and keeps it for the Task of the current
// directory. It is what the /baton-handoff skill calls, on the host and, as
// /baton/bin/baton, inside a sandbox.
func runHandoffSave(ctx context.Context, c console, home string, args []string) int {
	if len(args) != 0 {
		return c.usagef("handoff save: unexpected argument %q (%s)", args[0], handoffSaveUsage)
	}
	text, err := io.ReadAll(c.stdin)
	if err != nil {
		return c.errorf("handoff save: read standard input: %v", err)
	}
	dir, err := os.Getwd()
	if err != nil {
		return c.errorf("handoff save: %v", err)
	}

	saved, err := newHandoffSaver(home).Save(ctx, dir, string(text))
	if err != nil {
		return c.errorf("handoff save: %v", err)
	}
	c.println(toAppSaved(saved))
	return exitOK
}

// newHandoffSaver wires the domain. Where the Project's Baton directory is
// mounted (the container root is the one variable the tests of the sandbox
// set, see containerRootVar), it is a sandbox: the stores are those of the
// mounted directory. Otherwise it is the host.
func newHandoffSaver(home string) *handoffbus.Saver {
	mount := filepath.Join(os.Getenv(containerRootVar), runtimesandbox.ContainerProject)
	if info, err := os.Stat(mount); err == nil && info.IsDir() {
		return handoffbus.NewMountedSaver(
			handofffile.NewMounted(mount),
			projectbus.New(projectfile.NewMounted(mount)),
			taskbus.New(taskfile.NewMounted(mount)),
		)
	}
	batonDir := filepath.Join(home, ".baton")
	return handoffbus.NewSaver(
		handofffile.New(batonDir),
		projectbus.New(projectfile.New(batonDir)),
		taskbus.New(taskfile.New(batonDir)),
		newWorkBus(batonDir),
	)
}

// toAppSaved is the line that says where the narrative went.
func toAppSaved(s handoffbus.Saved) string {
	if s.Filled {
		return fmt.Sprintf("narrative saved into Handoff %d: %s", s.Handoff, s.Path)
	}
	return fmt.Sprintf("narrative saved as the draft for the next Handoff: %s", s.Path)
}
