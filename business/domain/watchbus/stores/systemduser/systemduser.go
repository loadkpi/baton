// Package systemduser installs `baton watch` as a systemd user service.
package systemduser

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/loadkpi/baton/foundation/atomicfile"
)

// UnitName is the name of the service.
const UnitName = "baton-watch.service"

// unit is the service file. %h is the home directory of the user, so the file
// does not hold a path of this machine; `make install` puts the binary there.
// Restart=on-failure brings the Daemon back if it dies on an error, and it keeps
// no state, so a restart loses nothing but what happened in between.
const unit = `[Unit]
Description=Baton: desktop notifications when an Agent needs you

[Service]
ExecStart=%h/.baton/bin/baton watch
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`

// timeout bounds each systemctl call.
const timeout = 30 * time.Second

// Installer writes the unit under the user's systemd configuration directory in
// home and asks `systemctl --user`, found on PATH, to load and start it.
type Installer struct {
	home string
}

// New creates the installer for the user whose home directory is home.
func New(home string) Installer { return Installer{home: home} }

// Installed says what Install did.
type Installed struct {
	// UnitPath is the file that was written; empty while it is not.
	UnitPath string
	// BinaryPath is the binary the unit starts, and BinaryMissing says it is
	// not there yet.
	BinaryPath    string
	BinaryMissing bool
}

// Install writes the unit file, then runs `systemctl --user daemon-reload` and
// `systemctl --user enable --now baton-watch.service`. The unit stays written
// when systemctl fails.
func (i Installer) Install(ctx context.Context) (Installed, error) {
	res := Installed{BinaryPath: filepath.Join(i.home, ".baton", "bin", "baton")}
	if _, err := os.Stat(res.BinaryPath); err != nil {
		res.BinaryMissing = true
	}
	path := filepath.Join(i.home, ".config", "systemd", "user", UnitName)
	if err := atomicfile.Write(path, []byte(unit), 0o644); err != nil {
		return res, err
	}
	res.UnitPath = path
	for _, args := range [][]string{{"--user", "daemon-reload"}, {"--user", "enable", "--now", UnitName}} {
		if err := systemctl(ctx, args...); err != nil {
			return res, err
		}
	}
	return res, nil
}

func systemctl(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "systemctl", args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(out.String()); msg != "" {
			return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
