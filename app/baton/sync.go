package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/configbus/stores/configfile"
	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/domain/projectbus/stores/projectfile"
	"github.com/loadkpi/baton/business/domain/runtimebus"
	"github.com/loadkpi/baton/business/domain/runtimebus/stores/runtimehost"
	"github.com/loadkpi/baton/business/domain/runtimebus/stores/runtimesandbox"
	"github.com/loadkpi/baton/business/domain/syncbus"
	"github.com/loadkpi/baton/business/domain/syncbus/stores/syncfile"
	"github.com/loadkpi/baton/business/types/agent"
)

// runSync implements `baton sync`.
func runSync(ctx context.Context, c console, home string) int {
	configPath := filepath.Join(home, ".config", "baton", "config.toml")
	cfg, err := configbus.New(configfile.New(configPath, home)).Load(ctx)
	if err != nil {
		return c.errorf("%v", err)
	}

	var targets []syncbus.Target
	var warnings []string
	for _, rt := range []runtimebus.Runtime{runtimehost.New(), runtimesandbox.New(projectbus.New(projectfile.New(filepath.Join(home, ".baton"))))} {
		t, w, err := rt.Targets(ctx, cfg)
		if err != nil {
			return c.errorf("%s Runtime: %v", rt.Name(), err)
		}
		targets = append(targets, t...)
		warnings = append(warnings, w...)
	}

	res, err := syncbus.New(syncfile.New(filepath.Join(home, ".baton"), filepath.Join(home, ".config", "baton", "secrets.env")), newConfirmer(c)).Sync(ctx, cfg, targets)
	if err != nil {
		return c.errorf("%v", err)
	}
	res.Warnings = append(warnings, res.Warnings...)

	for _, w := range res.Warnings {
		c.warnf("%s", w)
	}
	for _, line := range toAppSyncLines(res) {
		c.println(line)
	}
	return exitOK
}

// toAppSyncLines formats a sync result as one output line per Profile, and one
// per backup.
func toAppSyncLines(res syncbus.Result) []string {
	lines := make([]string, 0, len(res.Profiles))
	for _, p := range res.Profiles {
		for _, b := range p.Backups {
			lines = append(lines, fmt.Sprintf("%s: backed up %s to %s", p.Name, b.Path, b.Copy))
		}
		lines = append(lines, fmt.Sprintf("%s: %d files written, %d removed, %d unchanged", p.Name, p.Written, p.Removed, p.Unchanged))
		if p.HooksFirst {
			line := fmt.Sprintf("%s: status hooks installed; Sessions of this Profile that are already running keep their old hooks and must be restarted to report Events", p.Name)
			if p.Agent == agent.Codex {
				line += "; Codex runs hooks only after you review and trust them with /hooks"
			}
			lines = append(lines, line)
		}
	}
	return lines
}
