package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/loadkpi/baton/business/domain/awaybus"
	"github.com/loadkpi/baton/business/domain/awaybus/stores/awayfile"
	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/configbus/stores/configfile"
	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionfile"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionproc"
	"github.com/loadkpi/baton/business/domain/taskbus"
	"github.com/loadkpi/baton/business/domain/taskbus/stores/taskfile"
	"github.com/loadkpi/baton/business/domain/watchbus"
	"github.com/loadkpi/baton/business/domain/watchbus/stores/notifysend"
	"github.com/loadkpi/baton/business/domain/watchbus/stores/soundplay"
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

	// The signal ends the loop and nothing else. A look that has started,
	// and the notifications it decided to send, run on ctx, which a signal
	// does not cancel: stopping the watcher (systemd stop or restart) must not
	// kill a notify-send or a Telegram request in flight and print a warning
	// for it. Every delivery has its own time limit, so the wait is bounded.
	//
	// The first signal also gives the signals back to the system: a second
	// one ends the process at once, for a user who does not want to wait.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	stopping := make(chan struct{})
	var sending atomic.Int32 // notifications being sent right now
	finished := make(chan struct{})
	defer close(finished)
	defer signal.Stop(sigs)
	go func() {
		select {
		case <-sigs:
			signal.Stop(sigs)
			if sending.Load() > 0 {
				c.println("stopping: finishing the notification being sent (signal again to stop at once)")
			} else {
				c.println("stopping")
			}
			close(stopping)
		case <-finished:
		}
	}()

	// The [watch] table is read once, here: a wrong one stops the watcher
	// before it starts, and a change needs a restart.
	settings, err := configbus.New(configfile.New(configPath(home), home)).Watch(ctx)
	if err != nil {
		return c.errorf("watch: %v", err)
	}
	desktop, err := newDesktop(c, home, toAppWatchSettings(settings))
	if err != nil {
		return c.errorf("watch: %v", err)
	}
	bus := newWatchBus(home, readerConfig(ctx, c, home), desktop, &sending)
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
		case <-stopping:
			return exitOK
		case <-ticker.C:
		}
		select {
		case <-stopping:
			return exitOK // both were ready: no new look after a signal
		default:
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

// watchSettings are the settings of `baton watch`, as primitive types.
type watchSettings struct {
	sound string
}

func toAppWatchSettings(w configbus.Watch) watchSettings {
	return watchSettings{sound: w.Sound.String()}
}

// newDesktop is the desktop channel: notify-send, followed by the sound the
// config asks for. The sound is looked up now, so that a wrong one stops the
// watcher before it starts.
func newDesktop(c console, home string, settings watchSettings) (watchbus.Notifier, error) {
	desktop := watchbus.Notifier(notifysend.New())
	if settings.sound == "" {
		return desktop, nil
	}
	player, err := soundplay.New(home, settings.sound, func(msg string) { c.warnf("%s", msg) })
	if err != nil {
		return nil, fmt.Errorf("%s: watch.sound: %w", configPath(home), err)
	}
	if problem := player.Problem(); problem != "" {
		c.warnf("%s", problem)
	}
	return watchbus.WithSound(desktop, player), nil
}

// newWatchBus wires the watching domain over the files under home. The desktop
// is the channel, and Telegram takes over while `baton away` is in force.
// sending counts the notifications being sent through any channel.
func newWatchBus(home string, cfg configbus.Config, desktop watchbus.Notifier, sending *atomic.Int32) *watchbus.Bus {
	batonDir := filepath.Join(home, ".baton")
	rts := runtimes(home, cfg)
	sessions := sessionbus.New(sessionfile.New(batonDir), sessionproc.New()).WithRuntimes(rts)
	work := workbus.New(sessions, newProjects(home, cfg), taskbus.New(taskfile.New(batonDir)), rts)
	away := awaybus.New(awayfile.New(batonDir))
	return watchbus.New(sessions, work, countingNotifier{watchbus.NewRouter(desktop, telegram.New(telegramSource(home)), away), sending})
}

// countingNotifier counts the Notify calls that have not returned.
type countingNotifier struct {
	next    watchbus.Notifier
	sending *atomic.Int32
}

func (n countingNotifier) Notify(ctx context.Context, m watchbus.Message) error {
	n.sending.Add(1)
	defer n.sending.Add(-1)
	return n.next.Notify(ctx, m)
}
