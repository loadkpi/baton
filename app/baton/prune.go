package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionfile"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionproc"
)

const pruneUsage = "usage: baton prune --older-than <N>d [--dry-run]"

// maxPruneDays is the longest age that makes a difference: no Event file is a
// hundred years old, and a bigger number would overflow a duration.
const maxPruneDays = 36500

// pruneArgs are the words of `baton prune`, as typed.
type pruneArgs struct {
	olderThan string
	dryRun    bool
}

// runPrune implements `baton prune --older-than <N>d [--dry-run]`: it deletes
// the Event files of the Sessions that ended or were lost more than N days
// ago. It runs only when asked; nothing in Baton prunes by itself.
func runPrune(ctx context.Context, c console, home string, args []string) int {
	parsed, err := parsePruneArgs(args)
	if err != nil {
		return c.usagef("prune: %v (%s)", err, pruneUsage)
	}
	days, err := toBusDays(parsed.olderThan)
	if err != nil {
		return c.usagef("prune: %v (%s)", err, pruneUsage)
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

	bus := sessionbus.New(sessionfile.New(filepath.Join(home, ".baton")), sessionproc.New())
	// An error comes with what was done until then: the files already deleted
	// are still listed below.
	pruned, launches, pruneErr := bus.Prune(ctx, sessions, toBusAge(days), parsed.dryRun)
	if pruneErr != nil && len(pruned) == 0 {
		return c.errorf("%v", pruneErr)
	}

	n := sessionIDLenOf(listing.Sessions)
	w := tabwriter.NewWriter(c.stdout, 0, 0, 2, ' ', 0)
	files, bytes, failed := 0, int64(0), 0
	for _, p := range pruned {
		if p.Err != nil {
			failed++
			continue
		}
		if files == 0 {
			_, _ = fmt.Fprintln(w, "PROJECT\tTASK\tAGENT\tSESSION\tPROFILE\tRUNTIME\tSTATUS\tFILE")
		}
		files++
		bytes += p.Size
		for _, placed := range listing.Sessions {
			if placed.Session.Key() == p.Session.Key() {
				_, _ = fmt.Fprintln(w, joinCells(append(toAppSessionRow(placed, n), p.Path)))
			}
		}
	}
	_ = w.Flush()

	verb := "deleted"
	if parsed.dryRun {
		verb = "would delete"
	}
	switch {
	case files > 0:
		c.println(fmt.Sprintf("%s %d %s, %d bytes", verb, files, plural(files, "file"), bytes))
	case failed == 0:
		c.println("nothing to prune")
	}
	if len(launches) > 0 {
		c.println(fmt.Sprintf("removed %d launch %s", len(launches), plural(len(launches), "record")))
	}
	for _, p := range pruned {
		if p.Err != nil {
			c.errorf("cannot delete %s: %v", p.Path, toAppReason(p.Err))
		}
	}
	if pruneErr != nil {
		return c.errorf("%v", pruneErr)
	}
	if failed > 0 {
		return exitError
	}
	return exitOK
}

// toAppReason is the reason an operation on a file failed, without the
// operation and the path that the error repeats.
func toAppReason(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// parsePruneArgs splits the command line into its flags; it takes no other
// words.
func parsePruneArgs(args []string) (pruneArgs, error) {
	var out pruneArgs
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&out.olderThan, "older-than", "", "")
	fs.BoolVar(&out.dryRun, "dry-run", false, "")
	if err := fs.Parse(args); err != nil {
		return pruneArgs{}, err
	}
	seen := false
	fs.Visit(func(f *flag.Flag) { seen = seen || f.Name == "older-than" })
	switch {
	case fs.NArg() > 0:
		return pruneArgs{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	case !seen:
		return pruneArgs{}, errors.New("--older-than is required")
	}
	return out, nil
}

// toBusDays parses the age as typed: a whole number of days of 1 or more,
// with or without the d.
func toBusDays(s string) (int, error) {
	digits := strings.TrimSuffix(s, "d")
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("--older-than %q is not a whole number of days such as 90d", s)
		}
	}
	days, err := strconv.Atoi(digits)
	switch {
	case digits == "":
		return 0, fmt.Errorf("--older-than %q is not a whole number of days such as 90d", s)
	case err != nil:
		return maxPruneDays, nil // too big for an int, so older than anything
	case days < 1:
		return 0, fmt.Errorf("--older-than %q must be at least 1 day", s)
	}
	return min(days, maxPruneDays), nil
}

// toBusAge converts a number of days to the age Prune takes.
func toBusAge(days int) time.Duration {
	return time.Duration(days) * 24 * time.Hour
}
