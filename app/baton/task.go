package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/domain/projectbus/stores/projectfile"
	"github.com/loadkpi/baton/business/domain/taskbus"
	"github.com/loadkpi/baton/business/domain/taskbus/stores/taskfile"
	"github.com/loadkpi/baton/business/types/taskname"
	"github.com/loadkpi/baton/business/types/taskref"
	"github.com/loadkpi/baton/business/types/taskstatus"
)

const (
	taskNameUsage   = "usage: baton task name <project>/<task> <name>"
	taskStatusUsage = "usage: baton task status <project>/<task> <open|done|dropped>"
)

// runTask implements `baton task name` and `baton task status`, which change
// what a human decides about a Task.
func runTask(ctx context.Context, c console, home string, args []string) int {
	if len(args) == 0 {
		return c.usagef("task: a subcommand is required (available: name, status)")
	}
	switch args[0] {
	case "name":
		return runTaskName(ctx, c, home, args[1:])
	case "status":
		return runTaskStatus(ctx, c, home, args[1:])
	default:
		return c.usagef("task: unknown subcommand %q (available: name, status)", args[0])
	}
}

// runTaskName implements `baton task name <project>/<task> <name>`.
func runTaskName(ctx context.Context, c console, home string, args []string) int {
	if len(args) < 2 {
		return c.usagef("task name: a new name is required (%s)", taskNameUsage)
	}
	if len(args) > 2 {
		return c.usagef("task name: unexpected argument %q (%s)", args[2], taskNameUsage)
	}
	project, ref, err := toBusTaskPath(args[0])
	if err != nil {
		return c.usagef("task name: %v (%s)", err, taskNameUsage)
	}
	name, err := taskname.Parse(args[1])
	if err != nil {
		return c.usagef("task name: %v", err)
	}

	batonDir := filepath.Join(home, ".baton")
	p, err := projectbus.New(projectfile.New(batonDir)).Named(ctx, project)
	if err != nil {
		return c.errorf("%v", err)
	}
	t, err := taskbus.New(taskfile.New(batonDir)).Rename(ctx, p.Name, ref, name)
	if err != nil {
		return c.errorf("%v", err)
	}
	c.println(p.Name.String() + "/" + t.Label())
	return exitOK
}

// runTaskStatus implements `baton task status <project>/<task> <status>`.
func runTaskStatus(ctx context.Context, c console, home string, args []string) int {
	if len(args) < 2 {
		return c.usagef("task status: a status is required (%s)", taskStatusUsage)
	}
	if len(args) > 2 {
		return c.usagef("task status: unexpected argument %q (%s)", args[2], taskStatusUsage)
	}
	project, ref, err := toBusTaskPath(args[0])
	if err != nil {
		return c.usagef("task status: %v (%s)", err, taskStatusUsage)
	}
	status, err := taskstatus.Parse(args[1])
	if err != nil {
		return c.usagef("task status: %v (%s)", err, taskStatusUsage)
	}

	batonDir := filepath.Join(home, ".baton")
	p, err := projectbus.New(projectfile.New(batonDir)).Named(ctx, project)
	if err != nil {
		return c.errorf("%v", err)
	}
	t, err := taskbus.New(taskfile.New(batonDir)).SetStatus(ctx, p.Name, ref, status)
	if err != nil {
		return c.errorf("%v", err)
	}
	c.println(p.Name.String() + "/" + t.Label() + " " + t.Status.String())
	return exitOK
}

// toBusTaskPath parses <project>/<task>, as the tab title shows it, into the
// Project's name, which the Project domain looks up, and a reference to the
// Task by name or id.
func toBusTaskPath(s string) (project string, ref taskref.Ref, err error) {
	project, task, ok := strings.Cut(s, "/")
	if !ok || project == "" {
		return "", taskref.Ref{}, fmt.Errorf("want <project>/<task>, got %q", s)
	}
	ref, err = taskref.Parse(task)
	if err != nil {
		return "", taskref.Ref{}, err
	}
	return project, ref, nil
}
