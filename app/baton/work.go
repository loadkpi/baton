package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/configbus/stores/configfile"
	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/domain/projectbus/stores/projectfile"
	"github.com/loadkpi/baton/business/domain/runtimebus"
	"github.com/loadkpi/baton/business/domain/runtimebus/stores/runtimecompose"
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

// configPath is the Baton configuration file.
func configPath(home string) string {
	return filepath.Join(home, ".config", "baton", "config.toml")
}

// readerConfig loads the configuration for the commands that read Sessions and
// work without one (`baton ls`, `baton watch`, `baton handoff save`). Without a
// config file the result is the empty Config. A file that cannot be loaded is
// reported and the command goes on with the empty Config, which only means
// that a Project's Compose service is not known; it must not blind `baton ls`.
func readerConfig(ctx context.Context, c console, home string) configbus.Config {
	if _, err := os.Stat(configPath(home)); os.IsNotExist(err) {
		return configbus.Config{}
	}
	cfg, err := configbus.New(configfile.New(configPath(home), home)).Load(ctx)
	if err != nil {
		c.warnf("%v; Compose projects are not known", err)
		return configbus.Config{}
	}
	return cfg
}

// newProjects is the Project domain every reader of a Project's Runtime uses.
// It is the one place where the configuration overrides the recorded default
// Runtime (a Project with a Compose service runs there), so `baton new`,
// `baton handoff`, the active-Session guard and `baton ls` all agree.
func newProjects(home string, cfg configbus.Config) *projectbus.Bus {
	return projectbus.New(projectfile.New(filepath.Join(home, ".baton"))).WithDefaultRuntime(cfg.DefaultRuntime)
}

// newWorkBus wires the domains that place Sessions, over the files under home.
func newWorkBus(home string, cfg configbus.Config) *workbus.Bus {
	batonDir := filepath.Join(home, ".baton")
	rts := runtimes(home, cfg)
	return workbus.New(
		sessionbus.New(sessionfile.New(batonDir), sessionproc.New()).WithRuntimes(rts),
		newProjects(home, cfg),
		taskbus.New(taskfile.New(batonDir)),
		rts,
	)
}

// runtimes are the Runtimes that map a path inside their container to a host
// path and tell whether the Agent of a Session still runs.
func runtimes(home string, cfg configbus.Config) map[runtime.Runtime]runtimebus.Runtime {
	batonDir := filepath.Join(home, ".baton")
	projects := projectbus.New(projectfile.New(batonDir))
	return map[runtime.Runtime]runtimebus.Runtime{
		runtime.Host:    runtimehost.New(),
		runtime.Sandbox: runtimesandbox.New(projects, batonDir),
		runtime.Compose: runtimecompose.New(projects, cfg),
	}
}
