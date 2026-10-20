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

	cmd := exec.CommandContext(ctx, "notify-send", "-a", "baton", m.Summary(), m.Body())
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
