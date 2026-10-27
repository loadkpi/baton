package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/loadkpi/baton/business/domain/launchbus"
	"github.com/loadkpi/baton/business/domain/launchbus/stores/launchguake"
	"github.com/loadkpi/baton/foundation/rawterm"
	"github.com/loadkpi/baton/foundation/terminal"
)

const topUsage = "usage: baton top [--interval <duration>]"

// runTop implements `baton top`: a live table of the Sessions on the alternate
// screen, from which the user jumps to the Guake tab of a Session or ends one.
func runTop(ctx context.Context, c console, home string, args []string) int {
	fs := flag.NewFlagSet("top", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	interval := fs.Duration("interval", defaultInterval, "how often to read the Sessions")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return c.usagef("top: %s", topUsage)
	}
	if *interval <= 0 {
		return c.usagef("top: --interval must be positive")
	}
	in, _ := c.stdin.(*os.File)
	out, _ := c.stdout.(*os.File)
	if in == nil || out == nil || !terminal.IsTerminal(in) || !terminal.IsTerminal(out) {
		return c.usagef("top: standard input and output must be a terminal")
	}

	restore, err := rawterm.Enable(in)
	if err != nil {
		return c.errorf("top: %v", err)
	}
	_, _ = io.WriteString(out, enterScreen)
	// Whatever way out: the screen and the terminal go back as they were.
	defer func() {
		_, _ = io.WriteString(out, leaveScreen)
		_ = restore()
	}()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	t := &top{ctx: ctx, c: c, home: home, out: out, tabs: launchguake.New(), selected: -1}
	keys := readKeys(in)
	t.reload()
	t.draw()
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return exitOK
		case <-ticker.C:
			t.reload()
		case k, ok := <-keys:
			if !ok || t.key(k) {
				return exitOK
			}
		}
		t.draw()
	}
}

// readKeys reads the terminal and sends the keys typed. The channel closes
// when the terminal cannot be read any more.
func readKeys(in *os.File) <-chan rawterm.Key {
	keys := make(chan rawterm.Key)
	go func() {
		defer close(keys)
		buf := make([]byte, 64)
		for {
			n, err := in.Read(buf)
			for _, k := range rawterm.Decode(buf[:n]) {
				keys <- k
			}
			if err != nil {
				return
			}
		}
	}()
	return keys
}

// top is the state of the screen.
type top struct {
	ctx  context.Context
	c    console
	home string
	out  *os.File
	tabs launchbus.TabSwitcher

	lines []line
	// selected is the index of the selected row, -1 for none; selectedID is
	// the row it was made of, so that the selection stays on the same Session
	// when the rows change.
	selected   int
	selectedID string
	// confirming is the Session whose end the user is asked to confirm.
	confirming string
	message    string
}

// rowID identifies the Session or launch a row shows across reads.
func rowID(l line) string {
	if l.placed != nil {
		k := l.placed.Session.Key()
		return fmt.Sprintf("s:%s/%s/%s", k.Project, k.Agent, k.SessionID)
	}
	return fmt.Sprintf("l:%s@%s", l.launch.Title, l.launch.TS.Format(time.RFC3339Nano))
}

// reload reads the Sessions again; what goes wrong is told in the footer and
// the rows stay as they were.
func (t *top) reload() {
	listing, err := newWorkBus(t.home, readerConfig(t.ctx, quiet(t.c), t.home)).List(t.ctx)
	if err != nil {
		t.message = err.Error()
		return
	}
	if len(listing.Warnings) > 0 {
		t.message = "warning: " + listing.Warnings[0]
	}
	t.lines = toAppLines(listing)
	t.selected = -1
	for i, l := range t.lines {
		if rowID(l) == t.selectedID {
			t.selected = i
		}
	}
	if t.selected < 0 && len(t.lines) > 0 {
		t.selected = 0
		t.selectedID = rowID(t.lines[0])
	}
}

// quiet is a console whose output goes nowhere: the screen is not for it.
func quiet(c console) console {
	return console{stdin: c.stdin, stdout: io.Discard, stderr: io.Discard}
}

func (t *top) draw() {
	width, _ := rawterm.Size(t.out)
	s := topScreen{lines: t.lines, selected: t.selected, message: t.message, width: width}
	if t.confirming != "" {
		s.prompt = "End session " + t.confirming + "? [y/N]"
	}
	_, _ = io.WriteString(t.out, s.render())
}

// key handles one key and reports whether the user quits.
func (t *top) key(k rawterm.Key) (quit bool) {
	if k.Kind == rawterm.CtrlC {
		return true
	}
	if t.confirming != "" {
		id := t.confirming
		t.confirming = ""
		if k.Kind == rawterm.Rune && (k.Rune == 'y' || k.Rune == 'Y') {
			t.end(id)
		} else {
			t.message = "not ended"
		}
		return false
	}
	t.message = ""
	switch {
	case k.Kind == rawterm.Up || (k.Kind == rawterm.Rune && k.Rune == 'k'):
		t.move(-1)
	case k.Kind == rawterm.Down || (k.Kind == rawterm.Rune && k.Rune == 'j'):
		t.move(1)
	case k.Kind == rawterm.Enter:
		t.jump()
	case k.Kind == rawterm.Rune && k.Rune == 'e':
		t.askToEnd()
	case k.Kind == rawterm.Rune && k.Rune == 'q':
		return true
	}
	return false
}

func (t *top) move(delta int) {
	if len(t.lines) == 0 {
		return
	}
	t.selected = max(0, min(len(t.lines)-1, t.selected+delta))
	t.selectedID = rowID(t.lines[t.selected])
}

// current is the selected row.
func (t *top) current() (line, bool) {
	if t.selected < 0 || t.selected >= len(t.lines) {
		return line{}, false
	}
	return t.lines[t.selected], true
}

// tabTitles are the titles the tab of a row may have: the title `baton new`
// and `baton handoff` give it is <project>/<task>:<agent>, with the Task's
// name or its id, whichever the launch used.
func tabTitles(l line) []string {
	if l.launch != nil {
		return []string{l.launch.Title}
	}
	p := l.placed
	if !p.InGit {
		return nil
	}
	project, a := p.Place.Project.Name.String(), p.Session.Agent.String()
	titles := []string{project + "/" + p.Task.ID.String() + ":" + a}
	if label := p.Task.Label(); label != p.Task.ID.String() {
		titles = append([]string{project + "/" + label + ":" + a}, titles...)
	}
	return titles
}

func (t *top) jump() {
	l, ok := t.current()
	if !ok {
		return
	}
	err := launchbus.FocusTab(t.ctx, t.tabs, tabTitles(l))
	switch {
	case errors.Is(err, launchbus.ErrNoTab):
		t.message = "no Guake tab for this session (a session started by hand has none, or its tab was closed or renamed)"
	case err != nil:
		t.message = "cannot reach Guake: " + err.Error()
	default:
		t.message = "showing its Guake tab"
	}
}

func (t *top) askToEnd() {
	l, ok := t.current()
	if !ok {
		return
	}
	if l.placed == nil {
		t.message = "this launch has reported no events, there is no session to end"
		return
	}
	t.confirming = l.placed.Session.SessionID
}

// end ends the Session the way `baton session end` does. The Sessions are read
// again first, so that the Liveness it goes by is current.
func (t *top) end(sessionID string) {
	t.reload()
	for _, l := range t.lines {
		if l.placed == nil || l.placed.Session.SessionID != sessionID {
			continue
		}
		msg, err := endSession(t.ctx, t.home, l.placed.Session, false)
		if err != nil {
			msg = err.Error()
		}
		t.message = msg
		t.reload()
		return
	}
	t.message = "session " + sessionID + " is gone"
}
