// Package launchguake is the Terminal of Baton for Guake: it drives the
// `guake` executable.
//
// What was verified about Guake's command line is recorded in DESIGN.md, D8.
package launchguake

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/loadkpi/baton/business/domain/launchbus"
	"github.com/loadkpi/baton/foundation/shellquote"
)

// Terminal opens tabs in the running Guake.
type Terminal struct{}

// New creates the Terminal.
func New() *Terminal {
	return &Terminal{}
}

// OpenTab opens a tab, titles it and runs the command in tab.Dir, with one
// call: `guake -e <line> -r <title>`.
//
// Guake's -e opens a new tab (in its default directory, not in tab.Dir) and
// types the line into it; -r then renames the current tab, which is that new
// tab. The line is typed into the tab's interactive shell, so it is one shell
// line, quoted for it, that first changes into tab.Dir.
func (t *Terminal) OpenTab(ctx context.Context, tab launchbus.Tab) error {
	line := "cd " + shellquote.Join([]string{tab.Dir}) + " && " + shellquote.Join(tab.Command)
	return guake(ctx, "-e", line, "-r", tab.Title)
}

func guake(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "guake", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("guake: %w: %s", err, msg)
		}
		return fmt.Errorf("guake: %w", err)
	}
	return nil
}
