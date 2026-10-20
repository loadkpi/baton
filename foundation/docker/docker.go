// Package docker runs the docker command line client for the few questions
// Baton asks about containers that are already running.
package docker

import (
	"bytes"
	"context"
	"fmt"
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

func (c Client) run(ctx context.Context, args ...string) (string, error) {
	bin := c.Binary
	if bin == "" {
		bin = "docker"
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("docker %s: %w: %s", args[0], err, msg)
		}
		return "", fmt.Errorf("docker %s: %w", args[0], err)
	}
	return stdout.String(), nil
}

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
