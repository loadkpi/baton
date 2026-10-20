// Package procfs reads facts about processes from /proc (Linux).
package procfs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Process is what /proc tells about one process.
type Process struct {
	PID  int
	PPID int
	// Comm is the name the kernel keeps for the process (at most 15 bytes).
	Comm string
	// State is the one-letter state (field 3 of /proc/<pid>/stat): R, S, D, Z...
	State string
	// ExeBase is the base name of the executable the process runs; empty
	// when it cannot be read (another user's process, a kernel thread).
	ExeBase string
	// Start is when the process started, in clock ticks since boot (field
	// 22 of /proc/<pid>/stat). A reused PID has another start time.
	Start uint64
}

// Exited is true for a process that has finished but is still in /proc: a zombie
// (Z, killed and waiting for its parent to reap it) or dead (X). Its stat file
// exists, yet nothing runs.
func (p Process) Exited() bool {
	return p.State == "Z" || p.State == "X"
}

// Read returns the process with the given PID.
func Read(pid int) (Process, error) {
	dir := filepath.Join("/proc", strconv.Itoa(pid))
	data, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return Process{}, err
	}
	p, err := ParseStat(string(data))
	if err != nil {
		return Process{}, fmt.Errorf("%s/stat: %w", dir, err)
	}
	if exe, err := os.Readlink(filepath.Join(dir, "exe")); err == nil {
		p.ExeBase = filepath.Base(strings.TrimSuffix(exe, " (deleted)"))
	}
	return p, nil
}

// ParseStat reads the contents of /proc/<pid>/stat, "pid (comm) state ppid ...
// starttime ...". The name may itself hold spaces and parentheses, so the
// fields are counted from the last closing parenthesis.
func ParseStat(stat string) (Process, error) {
	open, closing := strings.IndexByte(stat, '('), strings.LastIndexByte(stat, ')')
	if open < 0 || closing < open {
		return Process{}, fmt.Errorf("unexpected format %q", stat)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(stat[:open]))
	if err != nil {
		return Process{}, err
	}
	// After the name: state (field 3), ppid (4), ..., starttime (22).
	rest := strings.Fields(stat[closing+1:])
	if len(rest) < 20 {
		return Process{}, fmt.Errorf("too few fields in %q", stat)
	}
	ppid, err := strconv.Atoi(rest[1])
	if err != nil {
		return Process{}, err
	}
	start, err := strconv.ParseUint(rest[19], 10, 64)
	if err != nil {
		return Process{}, err
	}
	return Process{PID: pid, PPID: ppid, Comm: stat[open+1 : closing], State: rest[0], Start: start}, nil
}

// containerFileRE matches the files of a Docker container's own directory that
// the engine bind-mounts into every container.
var containerFileRE = regexp.MustCompile(`/containers/([0-9a-f]{64})/(?:hostname|hosts|resolv\.conf)$`)

// cgroupRE matches the cgroup path of a container's processes, as the Docker
// engine (`/docker/<id>`, v1 and v2 with the host's cgroup namespace) and
// systemd (`docker-<id>.scope`) name it.
var cgroupRE = regexp.MustCompile(`(?:/docker/|/docker-)([0-9a-f]{64})(?:\.scope)?(?:/|$)`)

// ContainerID returns the id of the container a process runs in, from the
// text of its /proc/<pid>/mountinfo and /proc/<pid>/cgroup, or "" when neither
// shows one. The mount table is the better witness: the engine bind-mounts
// <state>/containers/<id>/{hostname,hosts,resolv.conf} at /etc/..., which a
// private cgroup namespace does not hide (it shows the cgroup as "/"). Only
// those three mounts count, so that a directory of another container that the
// user mounted somewhere is not mistaken for the container's own.
func ContainerID(mountinfo, cgroup string) string {
	for _, line := range strings.Split(mountinfo, "\n") {
		// id parent major:minor root mount-point options ...
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		switch fields[4] {
		case "/etc/hostname", "/etc/hosts", "/etc/resolv.conf":
		default:
			continue
		}
		if m := containerFileRE.FindStringSubmatch(fields[3]); m != nil {
			return m[1]
		}
	}
	for _, line := range strings.Split(cgroup, "\n") {
		// hierarchy:controllers:path
		if _, path, ok := strings.Cut(line, ":"); ok {
			if _, path, ok = strings.Cut(path, ":"); ok {
				if m := cgroupRE.FindStringSubmatch(path); m != nil {
					return m[1]
				}
			}
		}
	}
	return ""
}
