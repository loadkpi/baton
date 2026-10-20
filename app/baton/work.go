package main

import (
	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/domain/projectbus/stores/projectfile"
	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionfile"
	"github.com/loadkpi/baton/business/domain/sessionbus/stores/sessionproc"
	"github.com/loadkpi/baton/business/domain/taskbus"
	"github.com/loadkpi/baton/business/domain/taskbus/stores/taskfile"
	"github.com/loadkpi/baton/business/domain/workbus"
)

// newWorkBus wires the domains that place Sessions, over the files in batonDir.
func newWorkBus(batonDir string) *workbus.Bus {
	return workbus.New(
		sessionbus.New(sessionfile.New(batonDir), sessionproc.New()),
		projectbus.New(projectfile.New(batonDir)),
		taskbus.New(taskfile.New(batonDir)),
	)
}
