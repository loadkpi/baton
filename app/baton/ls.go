package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/loadkpi/baton/business/domain/sessionbus"
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

	listing, err := newWorkBus(batonDir).List(ctx)
	if err != nil {
		return c.errorf("%v", err)
	}
	for _, w := range listing.Warnings {
		c.warnf("%s", w)
	}
	if len(listing.Sessions) == 0 && len(listing.Silent) == 0 {
		c.println("no sessions")
		return exitOK
	}

	lines := toAppLines(listing)
	w := tabwriter.NewWriter(c.stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "PROJECT\tTASK\tAGENT\tPROFILE\tRUNTIME\tSTATUS")
	for _, l := range lines {
		_, _ = fmt.Fprintln(w, strings.Join(l.cells, "\t"))
	}
	_ = w.Flush()
	for _, l := range lines {
		if l.hint != "" {
			c.println("")
			break
		}
	}
	for _, l := range lines {
		if l.hint != "" {
			c.println(l.hint)
		}
	}
	return exitOK
}

// noEvents is the status shown for a Session Baton started that has reported
// no Event.
const noEvents = "no_events"

// line is one row of the table, and the hint that goes under it, if any.
type line struct {
	at    time.Time
	cells []string
	hint  string
}

// toAppLines converts the Sessions, and the launches that no Event followed, to
// rows, the most recent first.
func toAppLines(l workbus.Listing) []line {
	lines := make([]line, 0, len(l.Sessions)+len(l.Silent))
	for _, p := range l.Sessions {
		lines = append(lines, line{at: p.Session.Since, cells: toAppSessionRow(p)})
	}
	for _, s := range l.Silent {
		lines = append(lines, line{at: s.TS, cells: toAppLaunchRow(s), hint: toAppLaunchHint(s)})
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].at.After(lines[j].at) })
	return lines
}

// toAppLaunchRow converts a launch with no Event to the cells of its line.
func toAppLaunchRow(l sessionbus.Launch) []string {
	return []string{l.Project.String(), l.Task, l.Agent.String(), l.Profile.String(), l.Runtime.String(), noEvents}
}

// toAppLaunchHint says what to do about a Codex that is silent: it runs hooks
// only once they are trusted.
func toAppLaunchHint(l sessionbus.Launch) string {
	return fmt.Sprintf("%s has reported no events. Codex runs Baton's status hooks only after you trust them: run /hooks in that session.", l.Title)
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
