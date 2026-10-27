// Package launchproc finds the program a tab started, in /proc.
package launchproc

import (
	"os"
	"path/filepath"

	"github.com/loadkpi/baton/foundation/procfs"
)

// Processes looks at the processes of this machine.
type Processes struct{}

// New creates the store.
func New() *Processes {
	return &Processes{}
}

// Now is the current time on the clock that the start times of processes use.
func (*Processes) Now() (uint64, error) {
	return procfs.Now()
}

// Started tells whether a process of the user runs with its working directory
// in dir, under one of names, and started at or after since (a value of Now).
// A process is under a name when the name is that of the kernel (comm), of its
// executable, or the base name of the first or the second word of its command
// line: a script run by an interpreter shows the interpreter as the executable
// and, in the kernel, its own name, and a script run as `node <path>/claude`
// shows the script as the second word.
func (*Processes) Started(dir string, names []string, since uint64) (bool, error) {
	all, err := procfs.ReadAll()
	if err != nil {
		return false, err
	}
	dirs := []string{filepath.Clean(dir)}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dirs = append(dirs, real)
	}
	uid := os.Getuid()
	for _, p := range all {
		if p.UID != uid || p.Start < since || p.Exited() || !contains(dirs, p.Cwd) {
			continue
		}
		for _, n := range candidates(p) {
			if contains(names, n) {
				return true, nil
			}
		}
	}
	return false, nil
}

// candidates are the names a process answers to.
func candidates(p procfs.Detail) []string {
	out := []string{p.Comm, p.ExeBase}
	for i := 0; i < len(p.Args) && i < 2; i++ {
		if p.Args[i] != "" {
			out = append(out, filepath.Base(p.Args[i]))
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
