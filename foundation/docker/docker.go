// Package docker runs the docker command line client for the few questions
// Baton asks about containers that are already running.
package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/loadkpi/baton/foundation/procfs"
)

// timeout bounds every call, so that a daemon that does not answer cannot hang
// the command that asked.
const timeout = 20 * time.Second

// Client runs docker. The zero value runs the `docker` found on PATH.
type Client struct {
	// Binary is the docker executable; empty means "docker".
	Binary string
}

func (c Client) binary() string {
	if c.Binary == "" {
		return "docker"
	}
	return c.Binary
}

func (c Client) run(ctx context.Context, args ...string) (string, error) {
	out, err := c.exec(ctx, timeout, nil, args...)
	return string(out), err
}

// exec runs docker with args and the given standard input (none when nil) and
// returns standard output. A failure is an *Error, which carries the exit code
// and what docker wrote to standard error.
func (c Client) exec(ctx context.Context, limit time.Duration, stdin io.Reader, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.binary(), args...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		e := &Error{Command: args[0], Stderr: strings.TrimSpace(stderr.String()), Err: err}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			e.Code = exit.ExitCode()
		}
		return nil, e
	}
	return stdout.Bytes(), nil
}

// Error is a docker command that failed.
type Error struct {
	// Command is the docker subcommand ("compose", "cp", ...).
	Command string
	// Code is its exit code, or 0 when it did not run to an exit.
	Code int
	// Stderr is what it wrote to standard error, trimmed.
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("docker %s: %v: %s", e.Command, e.Err, e.Stderr)
	}
	return fmt.Sprintf("docker %s: %v", e.Command, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Running returns the full ids of the containers that run now, with one call.
func (c Client) Running(ctx context.Context) ([]string, error) {
	out, err := c.run(ctx, "ps", "--no-trunc", "--format", "{{.ID}}")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// readStats prints the stat file of each process whose PID is given, as the
// kernel of the container has it; a PID that runs nothing prints nothing. It ends
// with a successful command so that a failure of the script's last PID is not
// mistaken for a failure of docker.
const readStats = `for p in "$@"; do cat "/proc/$p/stat" 2>/dev/null; done; exit 0`

// ProcessStarts returns, for the PIDs that run in the container, their start
// times in clock ticks since boot (field 22 of /proc/<pid>/stat); a PID that
// does not run is not in the map, and a zombie or dead process (state Z or X,
// whose stat file is still there) does not run. It costs one `docker exec` whatever the
// number of PIDs, and needs only sh and cat in the container.
func (c Client) ProcessStarts(ctx context.Context, container string, pids []int) (map[int]uint64, error) {
	args := []string{"exec", container, "sh", "-c", readStats, "sh"}
	for _, pid := range pids {
		args = append(args, strconv.Itoa(pid))
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}

	starts := make(map[int]uint64, len(pids))
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		p, err := procfs.ParseStat(line)
		if err != nil {
			return nil, fmt.Errorf("docker exec %s: %w", container, err)
		}
		if p.Exited() {
			continue
		}
		starts[p.PID] = p.Start
	}
	return starts, nil
}
