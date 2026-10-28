package main

import (
	"context"
	"path/filepath"
	"time"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/namebus"
	"github.com/loadkpi/baton/business/domain/namebus/stores/namefile"
	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionfile"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionproc"
)

// noDetail is shown where a Session has no name or no last action.
const noDetail = "-"

// newNames makes the name domain over the files under home. Call Configure on
// it with the configuration of the moment before asking.
func newNames(home string) *namebus.Bus {
	batonDir := filepath.Join(home, ".baton")
	return namebus.New(namefile.New(), sessionbus.New(sessionfile.New(batonDir), sessionproc.New()))
}

// addDetails sets the name and the last action of every row that is a Session.
func addDetails(ctx context.Context, lines []line, names *namebus.Bus, now time.Time) {
	for i := range lines {
		l := &lines[i]
		l.name, l.last = noDetail, noDetail
		if l.placed == nil {
			continue
		}
		if n := names.Name(ctx, l.placed.Session); n != "" {
			l.name = n
		}
		if a := l.placed.Session.LastAction(now); a != "" {
			l.last = a
		}
	}
}

// configureNames lets names find the Profiles and map the paths of containers.
func configureNames(names *namebus.Bus, home string, cfg configbus.Config) {
	names.Configure(runtimes(home, cfg))
}
