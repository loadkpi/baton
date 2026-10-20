// Package sessionproc tells the Session domain about the machine it runs on,
// from /proc.
package sessionproc

import (
	"context"
	"os"
	"path/filepath"

	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/foundation/procfs"
)

// Machine reads the processes and the name of this machine.
type Machine struct {
	// root is the directory that stands for the root of the machine when
	// QueryContainer looks at its /proc/self; empty is the real root.
	root string
}

// New creates the reader.
func New() Machine { return Machine{} }

// NewIn creates the reader of a hook inside a container whose root is the
// directory root. It exists for the tests, which cannot write to /proc; the
// hook passes the value of the variable it reads for the mount of the Project
// (empty means "/").
func NewIn(root string) Machine { return Machine{root: root} }

// QueryProcess returns the process with the PID.
func (Machine) QueryProcess(_ context.Context, pid int) (sessionbus.Process, error) {
	p, err := procfs.Read(pid)
	if err != nil {
		return sessionbus.Process{}, err
	}
	return toBusProcess(p), nil
}

func toBusProcess(p procfs.Process) sessionbus.Process {
	return sessionbus.Process{PID: p.PID, PPID: p.PPID, Comm: p.Comm, ExeBase: p.ExeBase, Start: p.Start}
}

// QueryContainer returns the id of the container this process runs in, or ""
// when the kernel's files do not show one (a host, or a container runtime that
// hides it). Missing or unreadable files are "not known", not an error: nothing
// depends on it but a better check for lost Sessions.
func (m Machine) QueryContainer(_ context.Context) (string, error) {
	mountinfo, _ := os.ReadFile(filepath.Join(m.root, "/proc/self/mountinfo"))
	cgroup, _ := os.ReadFile(filepath.Join(m.root, "/proc/self/cgroup"))
	return procfs.ContainerID(string(mountinfo), string(cgroup)), nil
}

// QueryHostname returns the host name, which inside a container is the
// container id.
func (Machine) QueryHostname(_ context.Context) (string, error) {
	return os.Hostname()
}
