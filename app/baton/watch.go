package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/loadkpi/baton/business/domain/awaybus"
	"github.com/loadkpi/baton/business/domain/awaybus/stores/awayfile"
	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionfile"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionproc"
	"github.com/loadkpi/baton/business/domain/taskbus"
	"github.com/loadkpi/baton/business/domain/taskbus/stores/taskfile"
	"github.com/loadkpi/baton/business/domain/watchbus"
	"github.com/loadkpi/baton/business/domain/watchbus/stores/notifysend"
	"github.com/loadkpi/baton/business/domain/watchbus/stores/systemduser"
	"github.com/loadkpi/baton/business/domain/watchbus/stores/telegram"
	"github.com/loadkpi/baton/business/domain/workbus"
)

// defaultInterval is how often `baton watch` looks at the Sessions. A look
// reads the Event files and checks the recorded processes, so a short
// interval is cheap; it is also how late a lost Agent is noticed.
const defaultInterval = 2 * time.Second

const watchUsage = "usage: baton watch [--interval <duration>] | baton watch install"

// runWatchInstall implements `baton watch install`: it writes the systemd user
// unit of `baton watch` and enables and starts it.
func runWatchInstall(ctx context.Context, c console, home string, args []string) int {
	if len(args) != 0 {
		return c.usagef("watch: %s", watchUsage)
	}
	res, err := systemduser.New(home).Install(ctx)
	if res.UnitPath != "" {
		c.println("wrote " + res.UnitPath)
	}
	if res.BinaryMissing {
		c.warnf("%s does not exist yet; run make install, or the unit cannot start baton watch", res.BinaryPath)
	}
	if err != nil {
		return c.errorf("%v", err)
	}
	c.println("enabled and started " + systemduser.UnitName)
	return exitOK
}

// runWatch implements `baton watch`: the daemon that sends a desktop
// notification when an Agent needs the user. It runs until it is stopped.
func runWatch(ctx context.Context, c console, home string, args []string) int {
	if len(args) > 0 && args[0] == "install" {
		return runWatchInstall(ctx, c, home, args[1:])
	}
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	interval := fs.Duration("interval", defaultInterval, "how often to look at the Sessions")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return c.usagef("watch: %s", watchUsage)
	}
	if *interval <= 0 {
		return c.usagef("watch: --interval must be positive")
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	bus := newWatchBus(home, readerConfig(ctx, c, home))
	warned := warnings{c: c}
	n, ws, err := bus.Start(ctx)
	if err != nil {
		return c.errorf("watch: %v", err)
	}
	warned.show(ws)
	c.println(fmt.Sprintf("watching %d sessions every %s", n, *interval))

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return exitOK
		case <-ticker.C:
		}
		ws, err := bus.Look(ctx)
		if err != nil {
			ws = append(ws, err.Error())
		}
		warned.show(ws)
	}
}

// warnings prints each warning once while it lasts, so that a docker that stays
// down does not fill the journal with the same line every few seconds.
type warnings struct {
	c    console
	last map[string]bool
}

func (w *warnings) show(ws []string) {
	now := make(map[string]bool, len(ws))
	for _, m := range ws {
		now[m] = true
		if !w.last[m] {
			w.c.warnf("%s", m)
		}
	}
	w.last = now
}

// newWatchBus wires the watching domain over the files under home. The desktop
// is the channel, and Telegram takes over while `baton away` is in force.
func newWatchBus(home string, cfg configbus.Config) *watchbus.Bus {
	batonDir := filepath.Join(home, ".baton")
	rts := runtimes(home, cfg)
	sessions := sessionbus.New(sessionfile.New(batonDir), sessionproc.New()).WithRuntimes(rts)
	work := workbus.New(sessions, newProjects(home, cfg), taskbus.New(taskfile.New(batonDir)), rts)
	away := awaybus.New(awayfile.New(batonDir))
	return watchbus.New(sessions, work, watchbus.NewRouter(notifysend.New(), telegram.New(telegramSource(home)), away))
}
