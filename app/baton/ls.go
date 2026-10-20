package main

import (
	"context"
	"fmt"
	"path/filepath"
	"text/tabwriter"

	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/domain/projectbus/stores/projectfile"
	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionfile"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionproc"
)

// noProject is shown for a Session outside any git repository.
const noProject = "-"

// runLs implements `baton ls`: every Session with its Project, Agent, Profile,
// Runtime and status. Reading Sessions registers the Projects they are in.
func runLs(ctx context.Context, c console, home string, args []string) int {
	if len(args) != 0 {
		return c.usagef("ls takes no arguments")
	}
	batonDir := filepath.Join(home, ".baton")

	sessions, err := sessionbus.New(sessionfile.New(batonDir), sessionproc.New()).List(ctx)
	if err != nil {
		return c.errorf("%v", err)
	}
	if len(sessions) == 0 {
		c.println("no sessions")
		return exitOK
	}

	projects := projectbus.New(projectfile.New(batonDir))
	byDir := make(map[string]string) // directory -> Project name, so each is resolved once
	rows := make([][]string, 0, len(sessions))
	for _, s := range sessions {
		name, seen := byDir[s.CWD]
		if !seen {
			p, ok, err := projects.Resolve(ctx, s.CWD)
			if err != nil {
				return c.errorf("%v", err)
			}
			name = noProject
			if ok {
				name = p.Name.String()
			}
			byDir[s.CWD] = name
		}
		rows = append(rows, toAppSessionRow(s, name))
	}

	w := tabwriter.NewWriter(c.stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "PROJECT\tAGENT\tPROFILE\tRUNTIME\tSTATUS")
	for _, row := range rows {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", row[0], row[1], row[2], row[3], row[4])
	}
	_ = w.Flush()
	return exitOK
}

// toAppSessionRow converts a Session to the cells of its line.
func toAppSessionRow(s sessionbus.Session, project string) []string {
	return []string{project, s.Agent.String(), s.Profile.String(), s.Runtime.String(), s.Status.String()}
}
