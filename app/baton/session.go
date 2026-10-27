package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"text/tabwriter"

	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionfile"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionproc"
	"github.com/loadkpi/baton/business/domain/workbus"
)

const sessionEndUsage = "usage: baton session end <session> [--force]"

// runSession implements `baton session`, which changes what Baton knows about
// a Session.
func runSession(ctx context.Context, c console, home string, args []string) int {
	if len(args) == 0 {
		return c.usagef("session: a subcommand is required (available: end)")
	}
	switch args[0] {
	case "end":
		return runSessionEnd(ctx, c, home, args[1:])
	default:
		return c.usagef("session: unknown subcommand %q (available: end)", args[0])
	}
}

// sessionEndArgs are the words of `baton session end`, as typed.
type sessionEndArgs struct {
	session string
	force   bool
}

// runSessionEnd implements `baton session end <session> [--force]`: it records
// that a Session is over, for the one a hook never told so and Baton cannot
// find gone. <session> is what the SESSION column of `baton ls` shows, or the
// whole id.
func runSessionEnd(ctx context.Context, c console, home string, args []string) int {
	parsed, err := parseSessionEndArgs(args)
	if err != nil {
		return c.usagef("session end: %v (%s)", err, sessionEndUsage)
	}

	listing, err := newWorkBus(home, readerConfig(ctx, c, home)).List(ctx)
	if err != nil {
		return c.errorf("%v", err)
	}
	for _, w := range listing.Warnings {
		c.warnf("%s", w)
	}

	sessions := make([]sessionbus.Session, len(listing.Sessions))
	for i, p := range listing.Sessions {
		sessions[i] = p.Session
	}
	target, err := sessionbus.Resolve(sessions, parsed.session)
	if err != nil {
		return sessionNotResolved(c, listing.Sessions, err)
	}

	msg, err := endSession(ctx, home, target, parsed.force)
	if err != nil {
		return c.errorf("%v", err)
	}
	c.println(msg)
	return exitOK
}

// endSession ends the Session the way `baton session end` does and returns
// what to tell the user. A Session that has already ended is not an error. A
// running one is refused unless force is set, with the message that says how
// to go on.
func endSession(ctx context.Context, home string, target sessionbus.Session, force bool) (string, error) {
	bus := sessionbus.New(sessionfile.New(filepath.Join(home, ".baton")), sessionproc.New())
	err := bus.End(ctx, target, force)
	var running *sessionbus.RunningError
	switch {
	case errors.Is(err, sessionbus.ErrAlreadyEnded):
		return "session " + target.SessionID + " has already ended", nil
	case errors.As(err, &running):
		return "", fmt.Errorf("%v; quit that Agent yourself, or pass --force to end the Session anyway (baton never kills a process)", err)
	case err != nil:
		return "", err
	}
	return "ended session " + target.SessionID, nil
}

// sessionNotResolved reports an id that names no Session or several, listing
// the Sessions it could be the way `baton ls` does.
func sessionNotResolved(c console, all []workbus.Placed, err error) int {
	var ambiguous *sessionbus.AmbiguousError
	if !errors.As(err, &ambiguous) {
		return c.errorf("%v", err)
	}
	n := sessionIDLenOf(all)
	out := c.stderr
	_, _ = fmt.Fprintf(out, "baton: %v; give more of the id. Candidates:\n", err)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "PROJECT\tTASK\tAGENT\tSESSION\tPROFILE\tRUNTIME\tSTATUS")
	for _, p := range all {
		for _, cand := range ambiguous.Candidates {
			if p.Session.Key() == cand.Key() {
				row := toAppSessionRow(p, n)
				_, _ = fmt.Fprintln(w, joinCells(row))
			}
		}
	}
	_ = w.Flush()
	return exitError
}

// parseSessionEndArgs splits the command line into the id and the flag, which
// may stand before or after it.
func parseSessionEndArgs(args []string) (sessionEndArgs, error) {
	var out sessionEndArgs
	fs := flag.NewFlagSet("session end", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&out.force, "force", false, "")

	var positional []string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return sessionEndArgs{}, err
		}
		rest = fs.Args()
		if len(rest) > 0 {
			positional = append(positional, rest[0])
			rest = rest[1:]
		}
	}

	switch {
	case len(positional) == 0:
		return sessionEndArgs{}, errors.New("a session is required")
	case len(positional) > 1:
		return sessionEndArgs{}, fmt.Errorf("unexpected argument %q", positional[1])
	}
	out.session = positional[0]
	return out, nil
}
