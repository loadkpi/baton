package main

import (
	"context"
	"fmt"
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
// the start of its native session id, Profile, Runtime and status. Reading Sessions registers the Projects they are in.
func runLs(ctx context.Context, c console, home string, args []string) int {
	if len(args) != 0 {
		return c.usagef("ls takes no arguments")
	}

	listing, err := newWorkBus(home, readerConfig(ctx, c, home)).List(ctx)
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
	_, _ = fmt.Fprintln(w, "PROJECT\tTASK\tAGENT\tSESSION\tPROFILE\tRUNTIME\tSTATUS")
	for _, l := range lines {
		_, _ = fmt.Fprintln(w, joinCells(l.cells))
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

// noSession is shown as the session id of a launch that no Event has followed:
// the Agent has not told Baton its id yet.
const noSession = "-"

// minSessionIDLen is how many characters of a native session id the SESSION
// column shows, unless more are needed to tell two Sessions apart. The full id
// of a Session, as `baton new` and `baton handoff` print it, starts with it.
const minSessionIDLen = 8

// noEvents is the status shown for a Session Baton started that has reported
// no Event.
const noEvents = "no_events"

// line is one row of the table, and the hint that goes under it, if any.
type line struct {
	at    time.Time
	cells []string
	hint  string
	// Exactly one of these is set: the Session the row shows, or the launch
	// that no Event has followed.
	placed *workbus.Placed
	launch *sessionbus.Launch
}

// toAppLines converts the Sessions, and the launches that no Event followed, to
// rows, the most recent first.
func toAppLines(l workbus.Listing) []line {
	lines := make([]line, 0, len(l.Sessions)+len(l.Silent))
	n := sessionIDLenOf(l.Sessions)
	for i := range l.Sessions {
		p := &l.Sessions[i]
		lines = append(lines, line{at: p.Session.Since, cells: toAppSessionRow(*p, n), placed: p})
	}
	for i := range l.Silent {
		s := &l.Silent[i]
		lines = append(lines, line{at: s.TS, cells: toAppLaunchRow(*s), hint: toAppLaunchHint(*s), launch: s})
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].at.After(lines[j].at) })
	return lines
}

// joinCells is a row of the table as the tab writer takes it.
func joinCells(cells []string) string { return strings.Join(cells, "\t") }

// toAppLaunchRow converts a launch with no Event to the cells of its line.
func toAppLaunchRow(l sessionbus.Launch) []string {
	return []string{l.Project.String(), l.Task, l.Agent.String(), noSession, l.Profile.String(), l.Runtime.String(), noEvents}
}

// toAppLaunchHint says what to do about a Codex that is silent: it runs hooks
// only once they are trusted.
func toAppLaunchHint(l sessionbus.Launch) string {
	return fmt.Sprintf("%s has reported no events. Codex runs Baton's status hooks only after you trust them: run /hooks in that session.", l.Title)
}

// sessionIDLenOf is sessionIDLen of the ids of the Sessions: the length the
// SESSION column has for them.
func sessionIDLenOf(sessions []workbus.Placed) int {
	ids := make([]string, len(sessions))
	for i, p := range sessions {
		ids[i] = p.Session.SessionID
	}
	return sessionIDLen(ids)
}

// sessionIDLen is the number of characters of each session id that the SESSION
// column shows: minSessionIDLen, or the least longer length at which the
// shown starts of different ids are all different.
func sessionIDLen(ids []string) int {
	longest := 0
	for _, id := range ids {
		longest = max(longest, len(id))
	}
	for n := minSessionIDLen; n < longest; n++ {
		seen := make(map[string]string, len(ids))
		unique := true
		for _, id := range ids {
			short := id[:min(n, len(id))]
			if other, ok := seen[short]; ok && other != id {
				unique = false
				break
			}
			seen[short] = id
		}
		if unique {
			return n
		}
	}
	return max(longest, minSessionIDLen)
}

// toAppSessionRow converts a Session to the cells of its line; n is the
// length of the session id to show.
func toAppSessionRow(p workbus.Placed, n int) []string {
	project, task := noProject, noProject
	if p.InGit {
		project, task = p.Place.Project.Name.String(), p.Task.Label()
	}
	s := p.Session
	return []string{project, task, s.Agent.String(), s.SessionID[:min(n, len(s.SessionID))], s.Profile.String(), s.Runtime.String(), s.Status.String()}
}
