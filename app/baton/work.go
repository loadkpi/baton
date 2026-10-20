package main

import (
	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/domain/projectbus/stores/projectfile"
	"github.com/loadkpi/baton/business/domain/runtimebus"
	"github.com/loadkpi/baton/business/domain/runtimebus/stores/runtimehost"
	"github.com/loadkpi/baton/business/domain/runtimebus/stores/runtimesandbox"
	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionfile"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionproc"
	"github.com/loadkpi/baton/business/domain/taskbus"
	"github.com/loadkpi/baton/business/domain/taskbus/stores/taskfile"
	"github.com/loadkpi/baton/business/domain/workbus"
	"github.com/loadkpi/baton/business/types/runtime"
)

// newWorkBus wires the domains that place Sessions, over the files in batonDir.
func newWorkBus(batonDir string) *workbus.Bus {
	rts := runtimes(batonDir)
	return workbus.New(
		sessionbus.New(sessionfile.New(batonDir), sessionproc.New()).WithRuntimes(rts),
		projectbus.New(projectfile.New(batonDir)),
		taskbus.New(taskfile.New(batonDir)),
		rts,
	)
}

// runtimes are the Runtimes that map a path inside their container to a host
// path and tell whether the Agent of a Session still runs. The compose Runtime
// is not built yet.
func runtimes(batonDir string) map[runtime.Runtime]runtimebus.Runtime {
	return map[runtime.Runtime]runtimebus.Runtime{
		runtime.Host:    runtimehost.New(),
		runtime.Sandbox: runtimesandbox.New(projectbus.New(projectfile.New(batonDir)), batonDir),
	}
}
