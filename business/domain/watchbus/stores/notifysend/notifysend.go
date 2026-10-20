// Package notifysend shows notifications on the desktop with notify-send.
package notifysend

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/loadkpi/baton/business/domain/watchbus"
	"github.com/loadkpi/baton/business/types/status"
)

// timeout bounds a call, so that a desktop that does not answer cannot hold up
// the next look at the Sessions.
const timeout = 10 * time.Second

// Notifier runs the `notify-send` found on PATH.
type Notifier struct{}

// New creates the notifier.
func New() Notifier { return Notifier{} }

// Notify shows the message as a desktop notification of the application
// "baton".
func (Notifier) Notify(ctx context.Context, m watchbus.Message) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "notify-send", "-a", "baton", toFileSummary(m.Status), toFileBody(m))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("notify-send: %w: %s", err, msg)
		}
		return fmt.Errorf("notify-send: %w", err)
	}
	return nil
}

// toFileSummary is the title of the notification. It never starts with a dash,
// so notify-send cannot take it for an option.
func toFileSummary(s status.Status) string {
	switch s {
	case status.WaitingApproval:
		return "Baton: waiting for approval"
	case status.WaitingInput:
		return "Baton: waiting for input"
	case status.Lost:
		return "Baton: session lost"
	default:
		return "Baton: " + s.String()
	}
}

// toFileBody names the Task and the Agent. A Task name or a project name never
// starts with a dash, and a Session outside git is described without one, so
// the body cannot be taken for an option either.
func toFileBody(m watchbus.Message) string {
	if m.Task == "" {
		return m.Agent.String() + " (outside git)"
	}
	return m.Task + " (" + m.Agent.String() + ")"
}
