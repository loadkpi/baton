package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/loadkpi/baton/business/domain/handoffbus/stores/handofffile"
	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/secretsfile"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionfile"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionproc"
)

const scrubUsage = "usage: baton scrub [--dry-run]"

// runScrub implements `baton scrub [--dry-run]`: it masks the secrets in the
// Event files and the Handoffs that are already on disk, the way `baton hook`
// masks a new Event. It runs only when asked.
func runScrub(ctx context.Context, c console, home string, args []string) int {
	fs := flag.NewFlagSet("scrub", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dryRun := fs.Bool("dry-run", false, "")
	if err := fs.Parse(args); err != nil {
		return c.usagef("scrub: %v (%s)", err, scrubUsage)
	}
	if fs.NArg() > 0 {
		return c.usagef("scrub: unexpected argument %q (%s)", fs.Arg(0), scrubUsage)
	}

	batonDir := filepath.Join(home, ".baton")
	secrets := secretsfile.New(filepath.Join(home, ".config", "baton", "secrets.env"))
	bus := sessionbus.New(sessionfile.New(batonDir), sessionproc.New()).
		WithSecrets(secrets).
		WithTexts(handofffile.New(batonDir))
	// An error comes with what was done until then, which is listed below.
	scrubbed, scrubErr := bus.Scrub(ctx, *dryRun)

	files, lines, failed := 0, 0, 0
	for _, s := range scrubbed {
		if s.Err != nil {
			failed++
			continue
		}
		files++
		lines += s.Lines
		c.println(fmt.Sprintf("%s: %d %s", s.Path, s.Lines, plural(s.Lines, "line")))
	}

	verb := "scrubbed"
	if *dryRun {
		verb = "would scrub"
	}
	if files > 0 {
		c.println(fmt.Sprintf("%s %d %s, %d %s", verb, files, plural(files, "file"), lines, plural(lines, "line")))
	} else if failed == 0 && scrubErr == nil {
		c.println("nothing to scrub")
	}
	for _, s := range scrubbed {
		if s.Err != nil {
			c.errorf("cannot scrub %s: %v", s.Path, toAppReason(s.Err))
		}
	}
	if scrubErr != nil {
		return c.errorf("%v", scrubErr)
	}
	if failed > 0 {
		return exitError
	}
	return exitOK
}
