package runtimebus

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Containers is what the Runtimes that run Agents in containers need from the
// container engine to tell whether an Agent is still alive.
type Containers interface {
	// Running returns the full ids of the containers that run now.
	Running(ctx context.Context) ([]string, error)
	// ProcessStarts returns the start times (clock ticks since boot) of those
	// of pids that run in the container (a zombie does not), which is the PID
	// namespace they were recorded in. Missing PIDs are not in the map.
	ProcessStarts(ctx context.Context, container string, pids []int) (map[int]uint64, error)
}

// AliveInContainers is the liveness check of every container Runtime (sandbox
// and compose). A Process is found in a container in one of two ways:
//
//   - by Container, the full id its hook found for itself: the container is
//     among the running ones with exactly that id, or the Process is gone.
//     Nothing is matched by a prefix or a name, so a recreated container, which
//     has a new id, never stands in for the one it replaced, even when the
//     Compose file gives both the same host name;
//   - else by Host, the host name inside the container, which is the first 12
//     characters of the container id unless the container was given a name
//     (`hostname:`). Such a name is not an id and does not find anything.
//
// Then:
//
//   - The container is not among the running ones: the Process is gone.
//   - It is: the PID is read inside it, with one ProcessStarts call for all the
//     Processes of that container, and is alive when it runs with the recorded
//     start time.
//
// Containers is asked once for the running ones, and not at all when no Process
// names a container. A Process that names none (no Container, and a Host that
// does not look like a container id) or whose Host is the start of more than
// one container id is ProcessUnknown, and so are all the Processes that Docker
// failed to answer for, with the error.
func AliveInContainers(ctx context.Context, c Containers, procs []Process) ([]Liveness, error) {
	out := make([]Liveness, len(procs))
	var asked []int
	for i, p := range procs {
		if p.NamesContainer() {
			asked = append(asked, i)
		}
	}
	if len(asked) == 0 {
		return out, nil
	}

	running, err := c.Running(ctx)
	if err != nil {
		return out, err
	}

	byContainer := make(map[string][]int)
	for _, i := range asked {
		var matches []string
		for _, id := range running {
			if named := procs[i].Container; validContainerID(named) {
				if id == named {
					matches = append(matches, id)
				}
			} else if strings.HasPrefix(id, procs[i].Host) {
				matches = append(matches, id)
			}
		}
		switch len(matches) {
		case 0:
			out[i] = ProcessGone
		case 1:
			byContainer[matches[0]] = append(byContainer[matches[0]], i)
		}
	}

	containers := make([]string, 0, len(byContainer))
	for id := range byContainer {
		containers = append(containers, id)
	}
	sort.Strings(containers)

	var errs []error
	for _, id := range containers {
		indexes := byContainer[id]
		pids := make([]int, 0, len(indexes))
		for _, i := range indexes {
			pids = append(pids, procs[i].PID)
		}
		starts, err := c.ProcessStarts(ctx, id, pids)
		if err != nil {
			errs = append(errs, fmt.Errorf("container %s: %w", id[:12], err))
			continue
		}
		for _, i := range indexes {
			if start, ok := starts[procs[i].PID]; ok && start == procs[i].Start {
				out[i] = ProcessAlive
			} else {
				out[i] = ProcessGone
			}
		}
	}
	return out, errors.Join(errs...)
}

// looksLikeContainerID is true for 12 to 64 lower-case hexadecimal digits: a
// container id or its short form, which is what Docker gives a container as its
// host name unless told otherwise.
func looksLikeContainerID(s string) bool {
	if len(s) < 12 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// validContainerID is true for a full container id: 64 lower-case hexadecimal
// digits.
func validContainerID(s string) bool {
	return len(s) == 64 && looksLikeContainerID(s)
}
