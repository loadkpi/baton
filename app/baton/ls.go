package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/loadkpi/baton/business/domain/workbus"
)

// noProject is shown, as the Project and the Task, for a Session outside any
// git repository.
const noProject = "-"

// runLs implements `baton ls`: every Session with its Project, Task, Agent,
// Profile, Runtime and status. Reading Sessions registers the Projects they are in.
func runLs(ctx context.Context, c console, home string, args []string) int {
	if len(args) != 0 {
		return c.usagef("ls takes no arguments")
	}
	batonDir := filepath.Join(home, ".baton")

	placed, err := newWorkBus(batonDir).List(ctx)
	if err != nil {
		return c.errorf("%v", err)
	}
	if len(placed) == 0 {
		c.println("no sessions")
		return exitOK
	}

	rows := make([][]string, 0, len(placed))
	for _, p := range placed {
		rows = append(rows, toAppSessionRow(p))
	}

	w := tabwriter.NewWriter(c.stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "PROJECT\tTASK\tAGENT\tPROFILE\tRUNTIME\tSTATUS")
	for _, row := range rows {
		_, _ = fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	_ = w.Flush()
	return exitOK
}

// toAppSessionRow converts a Session to the cells of its line.
func toAppSessionRow(p workbus.Placed) []string {
	project, task := noProject, noProject
	if p.InGit {
		project, task = p.Place.Project.Name.String(), p.Task.Label()
	}
	s := p.Session
	return []string{project, task, s.Agent.String(), s.Profile.String(), s.Runtime.String(), s.Status.String()}
}
