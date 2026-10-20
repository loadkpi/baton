// Package sessionproc tells the Session domain about the machine it runs on,
// from /proc.
package sessionproc

import (
	"context"
	"os"

	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/foundation/procfs"
)

// Machine reads the processes and the name of this machine.
type Machine struct{}

// New creates the reader.
func New() Machine { return Machine{} }

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

// QueryHostname returns the host name, which inside a container is the
// container id.
func (Machine) QueryHostname(_ context.Context) (string, error) {
	return os.Hostname()
}
