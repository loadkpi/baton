package syncbus

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/loadkpi/baton/business/types/status"
)

// settingsFile is where a Claude Profile keeps its hooks, relative to the
// Profile directory.
const settingsFile = "settings.json"

// hookCommand is the command line of Baton's status hook in a host Profile.
// The binary lives in ~/.baton/bin, the place the same file is mounted from
// into sandboxes later (DESIGN.md).
func hookCommand(home, profile string) string {
	return shellWord(hookBinary(home)) + " hook claude --profile " + profile + " --runtime host"
}

// hookBinary is the executable the hooks call.
func hookBinary(home string) string {
	return filepath.Join(home, ".baton", "bin", "baton")
}

// shellWord quotes s for `sh -c`, which runs Claude's hook commands, unless it
// is made only of characters that need no quoting.
func shellWord(s string) string {
	if strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_/.-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// hookGroup is the matcher group Baton adds to hooks.<Event>: no matcher, so
// it applies to everything, and one command handler.
func hookGroup(command string) map[string]any {
	return map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": command}},
	}
}

// planHooks plans Baton's status hooks in the Profile's settings.json.
//
// hooks.<Event> is an array the user's own hooks live in, so Baton owns one
// element of it, the group with its command, and never anything else. The
// rules of the shared file are those of planJSONKeys.
func (b *Bus) planHooks(ctx context.Context, plan *profilePlan, home string, previous Manifest) error {
	command := hookCommand(home, plan.profile.Name.String())

	var keys []wantedKey
	for _, event := range status.HookEvents(plan.profile.Agent) {
		keys = append(keys, wantedKey{Key: []string{"hooks", event}, Item: command, Value: hookGroup(command)})
	}

	// A hook recorded earlier whose command or Event is no longer the one
	// sync wants is taken out: the Profile was renamed, or Baton moved.
	var stale []OwnedKey
	for _, k := range previous.Keys {
		if k.Path != settingsFile || len(k.Key) != 2 || k.Key[0] != "hooks" || k.Item == "" {
			continue
		}
		if !hasKey(keys, k) {
			stale = append(stale, k)
		}
	}

	plan.hooks = len(keys) > 0
	// The first time Baton's hooks reach a Profile, Sessions that are already
	// running do not have them.
	plan.hooksFirst = plan.hooks && !ownsAnyIn(previous, settingsFile)

	return b.planJSONKeys(ctx, plan, previous, jsonTarget{
		rel:       settingsFile,
		backupRel: settingsFile,
		name:      settingsFile,
		wanted:    keys,
		stale:     stale,
		mode:      0o644,
	})
}

func hasKey(keys []wantedKey, k OwnedKey) bool {
	for _, w := range keys {
		if w.Item == k.Item && slices.Equal(w.Key, k.Key) {
			return true
		}
	}
	return false
}
