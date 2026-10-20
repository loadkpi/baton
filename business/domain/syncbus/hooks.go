package syncbus

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/runtime"
	"github.com/loadkpi/baton/business/types/status"
)

// settingsFile is where a Claude Profile keeps its hooks, relative to the
// Profile directory.
const settingsFile = "settings.json"

// codexHooksFile is where a Codex Profile keeps its hooks, relative to the
// Profile directory.
const codexHooksFile = "hooks.json"

// hookCommand is the command line of Baton's status hook: the binary as the
// Agent sees it, the Agent, the Profile and the Runtime it runs in.
//
// In a sandbox the line is guarded: an ai-sandbox that was started without
// Baton does not have the binary, and its hooks must then do nothing and
// succeed (ADR 0001). `exec` leaves no shell between the Agent and `baton
// hook`. Claude Code runs a hook command with `sh -c`; the Codex documentation
// does not say whether Codex uses a shell (DESIGN.md, ticket 09), so Codex gets
// the shell explicitly. Host hooks are not guarded: `baton sync` warns when
// the host binary is missing.
func hookCommand(binary string, a agent.Agent, profile string, rt runtime.Runtime) string {
	call := shellWord(binary) + " hook " + a.String() + " --profile " + profile + " --runtime " + rt.String()
	if rt != runtime.Sandbox {
		return call
	}
	guarded := "[ -x " + shellWord(binary) + " ] || exit 0; exec " + call
	if a == agent.Codex {
		return "sh -c " + shellQuote(guarded)
	}
	return guarded
}

// shellQuote always wraps s in single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// hostHookBinary is the executable the hooks of host Profiles call, which is
// also the directory that is mounted read-only into sandboxes.
func hostHookBinary(home string) string {
	return filepath.Join(home, ".baton", "bin", "baton")
}

// shellWord quotes s for `sh -c`, which runs Claude's hook commands, unless it
// is made only of characters that need no quoting.
func shellWord(s string) string {
	if strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_/.-") == "" {
		return s
	}
	return shellQuote(s)
}

// hookGroup is the matcher group Baton adds to hooks.<Event>: no matcher, so
// it applies to everything, and one command handler.
func hookGroup(command string) map[string]any {
	return map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": command}},
	}
}

// planHooks plans Baton's status hooks in the Profile's hooks file
// (settings.json for Claude, hooks.json for Codex).
//
// hooks.<Event> is an array the user's own hooks live in, so Baton owns one
// element of it, the group with its command, and never anything else. The
// rules of the shared file are those of planJSONKeys.
func (b *Bus) planHooks(ctx context.Context, plan *profilePlan, previous Manifest) error {
	file := plan.layout.hooksFile
	command := hookCommand(plan.binary, plan.profile.Agent, plan.profile.Name.String(), plan.runtime)

	var keys []wantedKey
	for _, event := range status.HookEvents(plan.profile.Agent) {
		keys = append(keys, wantedKey{Key: []string{"hooks", event}, Item: command, Value: hookGroup(command)})
	}

	// A hook recorded earlier whose command or Event is no longer the one
	// sync wants is taken out: the Profile was renamed, or Baton moved.
	var stale []OwnedKey
	for _, k := range previous.Keys {
		if k.Path != file || len(k.Key) != 2 || k.Key[0] != "hooks" || k.Item == "" {
			continue
		}
		if !hasKey(keys, k) {
			stale = append(stale, k)
		}
	}

	plan.hooks = len(keys) > 0
	// The first time Baton's hooks reach a Profile, Sessions that are already
	// running do not have them.
	plan.hooksFirst = plan.hooks && !ownsAnyIn(previous, file)

	return b.planJSONKeys(ctx, plan, previous, jsonTarget{
		rel:       file,
		backupRel: file,
		name:      file,
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
