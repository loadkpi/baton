package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/configbus/stores/configfile"
	"github.com/loadkpi/baton/business/domain/syncbus"
	"github.com/loadkpi/baton/business/domain/syncbus/stores/syncfile"
)

// runSync implements `baton sync`.
func runSync(ctx context.Context, c console, home string) int {
	configPath := filepath.Join(home, ".config", "baton", "config.toml")
	cfg, err := configbus.New(configfile.New(configPath, home)).Load(ctx)
	if err != nil {
		return c.errorf("%v", err)
	}

	res, err := syncbus.New(syncfile.New(filepath.Join(home, ".baton")), newConfirmer(c)).Sync(ctx, cfg)
	if err != nil {
		return c.errorf("%v", err)
	}

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
	}
	return lines
}
