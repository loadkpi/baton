package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/loadkpi/baton/internal/batontest"
)

// statusEvents are the Claude Code Events of the status table, in the order
// they are sorted in settings.json.
var statusEvents = []string{
	"PermissionRequest", "PostToolUse", "PostToolUseFailure", "PreToolUse",
	"SessionEnd", "SessionStart", "Stop", "UserPromptSubmit",
}

// hookCommand is the command sync writes for a host Profile.
func hookCommand(env *batontest.Env, profile string) string {
	return env.Path(".baton", "bin", "baton") + " hook claude --profile " + profile + " --runtime host"
}

// hookCommands returns, per Event of settings.json, the commands of all hook
// handlers, in file order.
func hookCommands(t *testing.T, env *batontest.Env, path string) map[string][]string {
	t.Helper()
	return hookCommandsOf(t, env.ReadFile(path), path)
}

// hookCommandsOf is hookCommands for the text of a hooks file, named for errors.
func hookCommandsOf(t *testing.T, text, name string) map[string][]string {
	t.Helper()
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("%s is not valid JSON: %v\n%s", name, err, text)
	}
	out := make(map[string][]string)
	for event, groups := range doc.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if h.Type == "command" {
					out[event] = append(out[event], h.Command)
				}
			}
		}
	}
	return out
}

func TestHooksSync_CreatesSettingsWithAStatusHookForEveryEventOfTheStatusTable(t *testing.T) {
	env := newEnv(t)
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.CommitSource("add skill")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := make(map[string][]string)
	for _, event := range statusEvents {
		want[event] = []string{hookCommand(env, "claude-personal")}
	}
	if got := hookCommands(t, env, filepath.Join(profile, "settings.json")); !reflect.DeepEqual(got, want) {
		t.Errorf("hooks = %v, want %v", got, want)
	}
}

// userSettings is a settings.json that belongs to the user: settings of their
// own and hooks of their own on two of the Events Baton uses, indented with
// tabs.
const userSettings = "{\n" +
	"\t\"theme\": \"dark\",\n" +
	"\t\"hooks\": {\n" +
	"\t\t\"Stop\": [\n" +
	"\t\t\t{\n" +
	"\t\t\t\t\"hooks\": [{\"type\": \"command\", \"command\": \"notify-me\"}]\n" +
	"\t\t\t}\n" +
	"\t\t],\n" +
	"\t\t\"PreToolUse\": [\n" +
	"\t\t\t{\"matcher\": \"Bash\", \"hooks\": [{\"type\": \"command\", \"command\": \"audit-bash\"}]}\n" +
	"\t\t]\n" +
	"\t},\n" +
	"\t\"model\": \"opus\"\n" +
	"}\n"

// withSettings sets up a Profile whose settings.json is the given text.
func withSettings(t *testing.T, env *batontest.Env, settings string) (profile, path string) {
	t.Helper()
	profile = env.MakeProfileDir(".claude-personal")
	path = filepath.Join(profile, "settings.json")
	env.WriteFile(path, settings)
	env.WriteConfig(standardConfig(env))
	return profile, path
}

func TestHooksSync_ExistingSettingsWithoutTerminalStopAndAreLeftAlone(t *testing.T) {
	env := newEnv(t)
	_, path := withSettings(t, env, userSettings)

	res := env.Run("sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, path) || !strings.Contains(res.Stderr, "terminal") {
		t.Errorf("stderr = %q, want it to name %s and say there is no terminal", res.Stderr, path)
	}
	if got := env.ReadFile(path); got != userSettings {
		t.Errorf("settings.json = %q, want it unchanged", got)
	}
	if env.Exists(".baton") {
		t.Error("sync wrote Baton files although it could not ask")
	}
}

func TestHooksSync_AddsBatonsHooksAfterBackupAndNeverDropsTheUsersOwn(t *testing.T) {
	env := newEnv(t)
	profile, path := withSettings(t, env, userSettings)

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	cmd := hookCommand(env, "claude-personal")
	want := map[string][]string{"Stop": {"notify-me", cmd}, "PreToolUse": {"audit-bash", cmd}}
	for _, event := range statusEvents {
		if _, ok := want[event]; !ok {
			want[event] = []string{cmd}
		}
	}
	if got := hookCommands(t, env, path); !reflect.DeepEqual(got, want) {
		t.Errorf("hooks = %v, want %v", got, want)
	}
	// Everything but Baton's entries is byte for byte what the user had.
	for _, kept := range []string{"\t\"theme\": \"dark\",\n", "\t\t\t\t\"hooks\": [{\"type\": \"command\", \"command\": \"notify-me\"}]\n", "\t\t\t{\"matcher\": \"Bash\", \"hooks\": [{\"type\": \"command\", \"command\": \"audit-bash\"}]}", "\t\"model\": \"opus\"\n"} {
		if !strings.Contains(env.ReadFile(path), kept) {
			t.Errorf("settings.json lost %q:\n%s", kept, env.ReadFile(path))
		}
	}
	if got := env.ReadFile(filepath.Join(onlyBackup(t, env, "claude-personal"), "settings.json")); got != userSettings {
		t.Errorf("backed up settings.json = %q, want the original", got)
	}
	for _, want := range []string{"hooks.Stop (Baton's entry)", "hooks.UserPromptSubmit (Baton's entry)", cmd} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("transcript lacks %q:\n%s", want, res.Stdout)
		}
	}
	if strings.Contains(res.Stdout, "notify-me") {
		t.Errorf("the question shows the user's own hook, it should show only Baton's entries:\n%s", res.Stdout)
	}
	_ = profile
}

// afterFirstHooksSync runs the first sync over the given settings.json,
// answering yes.
func afterFirstHooksSync(t *testing.T, env *batontest.Env, settings string) string {
	t.Helper()
	_, path := withSettings(t, env, settings)
	if res := env.RunTerminal([]string{"y"}, "sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	return path
}

func TestHooksSync_SecondSyncAsksNothingAndRewritesNothing(t *testing.T) {
	env := newEnv(t)
	path := afterFirstHooksSync(t, env, userSettings)
	before := env.ReadFile(path)
	modTime := env.ModTime(path)

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("second sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := env.ReadFile(path); got != before {
		t.Errorf("settings.json = %q, want it unchanged", got)
	}
	if !env.ModTime(path).Equal(modTime) {
		t.Error("the second sync rewrote settings.json")
	}
	if got := len(env.ListDir(".baton/backups/claude-personal")); got != 1 {
		t.Errorf("%d backups, want only the one of the first sync", got)
	}
}

func TestHooksSync_AsksAgainWhenBatonsHookWasEditedByHandAndRestoresOnlyIt(t *testing.T) {
	env := newEnv(t)
	path := afterFirstHooksSync(t, env, userSettings)
	cmd := hookCommand(env, "claude-personal")
	// The user adds a matcher to Baton's group on Stop.
	edited := strings.Replace(env.ReadFile(path), "\"hooks\": [\n\t\t\t\t\t{\n\t\t\t\t\t\t\"command\": \""+cmd, "\"matcher\": \"*\",\n\t\t\t\t\"hooks\": [\n\t\t\t\t\t{\n\t\t\t\t\t\t\"command\": \""+cmd, 1)
	if edited == env.ReadFile(path) {
		t.Fatal("the test did not edit settings.json")
	}
	env.WriteFile(path, edited)

	res := env.Run("sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, path) || !strings.Contains(res.Stderr, "terminal") {
		t.Errorf("stderr = %q, want it to name %s and say there is no terminal", res.Stderr, path)
	}
	if got := env.ReadFile(path); got != edited {
		t.Errorf("settings.json = %q, want the hand-edited version kept", got)
	}

	answered := env.RunTerminal([]string{"y"}, "sync")

	if answered.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", answered.ExitCode, answered.Stdout)
	}
	if !strings.Contains(answered.Stdout, `-  "matcher": "*"`) {
		t.Errorf("transcript does not show the matcher that goes:\n%s", answered.Stdout)
	}
	if got := env.ReadFile(path); strings.Contains(got, `"matcher": "*"`) || !strings.Contains(got, "audit-bash") {
		t.Errorf("settings.json = %q, want Baton's group restored and the user's hooks kept", got)
	}
}

func TestHooksSync_AsksAgainWhenBatonsHookWasCutOutByHand(t *testing.T) {
	env := newEnv(t)
	path := afterFirstHooksSync(t, env, userSettings)
	env.WriteFile(path, userSettings)

	res := env.Run("sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, path) {
		t.Errorf("stderr = %q, want it to name %s", res.Stderr, path)
	}
}

func TestHooksSync_EditorFormats(t *testing.T) {
	cmd := func(env *batontest.Env) string { return hookCommand(env, "claude-personal") }
	tests := []struct {
		name     string
		settings string
		// user is what the user's own hooks must be after the sync.
		user map[string][]string
	}{
		{"an empty object", "{}\n", nil},
		{"no hooks key and one line", `{"theme":"dark"}` + "\n", nil},
		{"hooks is an empty object", "{\n  \"hooks\": {}\n}\n", nil},
		{"an empty array", "{\n  \"hooks\": {\n    \"Stop\": []\n  }\n}\n", nil},
		{"an array on one line", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"mine"}]}]}}` + "\n", map[string][]string{"Stop": {"mine"}}},
		{"two groups on one line", `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "a"}]}, {"hooks": [{"type": "command", "command": "b"}]}]}}` + "\n", map[string][]string{"Stop": {"a", "b"}}},
		{"a hook with other handler types", `{"hooks": {"Stop": [{"hooks": [{"type": "http", "url": "https://x.invalid"}]}]}}` + "\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t)
			_, path := withSettings(t, env, tt.settings)

			if res := env.RunTerminal([]string{"y"}, "sync"); res.ExitCode != 0 {
				t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
			}

			got := hookCommands(t, env, path)
			for _, event := range statusEvents {
				want := append(append([]string{}, tt.user[event]...), cmd(env))
				if !reflect.DeepEqual(got[event], want) {
					t.Errorf("hooks.%s = %v, want %v", event, got[event], want)
				}
			}
			var doc map[string]any
			if err := json.Unmarshal([]byte(env.ReadFile(path)), &doc); err != nil {
				t.Errorf("settings.json is not valid JSON: %v\n%s", err, env.ReadFile(path))
			}

			// A second sync finds Baton's hooks again, wherever the editor put them.
			before := env.ReadFile(path)
			if res := env.Run("sync"); res.ExitCode != 0 || env.ReadFile(path) != before {
				t.Errorf("second sync: exit code = %d, stderr=%q, changed=%v", res.ExitCode, res.Stderr, env.ReadFile(path) != before)
			}
		})
	}
}

func TestHooksSync_OneLineDocumentsStayOnOneLine(t *testing.T) {
	env := newEnv(t)
	_, path := withSettings(t, env, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"mine"}]}]},"theme":"dark"}`+"\n")

	if res := env.RunTerminal([]string{"y"}, "sync"); res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}

	got := env.ReadFile(path)
	if strings.Count(got, "\n") != 1 {
		t.Errorf("settings.json = %q, want it still on one line", got)
	}
	if !strings.HasPrefix(got, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"mine"}]},{"hooks":[{"command":"`) || !strings.HasSuffix(got, `"theme":"dark"}`+"\n") {
		t.Errorf("settings.json = %q", got)
	}
}

func TestHooksSync_SettingsThatCannotBeEditedStopSyncNamingTheFile(t *testing.T) {
	for name, settings := range map[string]string{
		"not JSON":               "{ nope",
		"hooks is not an object": `{"hooks": []}` + "\n",
		"an Event is not a list": `{"hooks": {"Stop": {"hooks": []}}}` + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			env := newEnv(t)
			_, path := withSettings(t, env, settings)

			res := env.RunTerminal(nil, "sync")

			if res.ExitCode == 0 {
				t.Fatalf("exit code = 0, want non-zero; transcript:\n%s", res.Stdout)
			}
			if !strings.Contains(res.Stdout, path) {
				t.Errorf("transcript = %q, want it to name %s", res.Stdout, path)
			}
			if got := env.ReadFile(path); got != settings {
				t.Errorf("settings.json = %q, want it unchanged", got)
			}
		})
	}
}

func TestHooksSync_AHookBatonNoLongerWantsGoesWithoutAQuestionAndTheUsersOwnStay(t *testing.T) {
	env := newEnv(t)
	cmd := hookCommand(env, "claude-personal")
	old := "/old/baton hook claude --profile claude-personal --runtime host"
	// An earlier Baton wrote its hook for Stop from another place, between two
	// hooks of the user, and recorded it.
	settings := "{\n" +
		"  \"hooks\": {\n" +
		"    \"Stop\": [\n" +
		"      {\"hooks\": [{\"type\": \"command\", \"command\": \"first\"}]},\n" +
		"      {\"hooks\": [{\"type\": \"command\", \"command\": \"" + old + "\"}]},\n" +
		"      {\"hooks\": [{\"type\": \"command\", \"command\": \"last\"}]}\n" +
		"    ]\n" +
		"  }\n" +
		"}\n"
	_, path := withSettings(t, env, settings)
	env.WriteFile(".baton/manifests/claude-personal.json", `{
  "version": 1,
  "profile": "claude-personal",
  "files": [],
  "keys": [
    {
      "path": "settings.json",
      "key": ["hooks", "Stop"],
      "item": "`+old+`",
      "sha256": "810ff2d81eafbc1ed9b2479f381b108c3a28a28582c64bf6f88390499ac4a05a"
    }
  ]
}
`)

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := map[string][]string{"Stop": {"first", "last", cmd}}
	for _, event := range statusEvents {
		if _, ok := want[event]; !ok {
			want[event] = []string{cmd}
		}
	}
	if got := hookCommands(t, env, path); !reflect.DeepEqual(got, want) {
		t.Errorf("hooks = %v, want %v", got, want)
	}
	if env.Exists(".baton/backups") {
		t.Error("sync made a backup although it only replaced its own hook")
	}
}

func TestHooksSync_ManifestRecordsEachHookByItsCommand(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))

	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	var m struct {
		Keys []struct {
			Path string   `json:"path"`
			Key  []string `json:"key"`
			Item string   `json:"item"`
			Sum  string   `json:"sha256"`
		} `json:"keys"`
	}
	if err := json.Unmarshal([]byte(env.ReadFile(".baton/manifests/claude-personal.json")), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Keys) != len(statusEvents) {
		t.Fatalf("manifest has %d keys, want %d: %+v", len(m.Keys), len(statusEvents), m.Keys)
	}
	for i, k := range m.Keys {
		if k.Path != "settings.json" || !reflect.DeepEqual(k.Key, []string{"hooks", statusEvents[i]}) || k.Item != hookCommand(env, "claude-personal") || len(k.Sum) != 64 {
			t.Errorf("key %d = %+v", i, k)
		}
	}
}

func TestHooksSync_NeverWritesSettingsThatGitTracks(t *testing.T) {
	env := newEnv(t)
	profile, path := withSettings(t, env, "{}\n")
	gitInit(t, env, profile)

	res := env.Run("sync")

	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "git tracks") || !strings.Contains(res.Stderr, path) {
		t.Errorf("exit code = %d, stderr=%q, want a refusal naming %s", res.ExitCode, res.Stderr, path)
	}
	if got := env.ReadFile(path); got != "{}\n" {
		t.Errorf("settings.json = %q, want it unchanged", got)
	}
}

func TestHooksSync_EveryHostClaudeProfileGetsItsOwnHooks(t *testing.T) {
	env := newEnv(t)
	personal := env.MakeProfileDir(".claude-personal")
	work := env.MakeProfileDir(".claude-work")
	env.WriteConfig(twoProfilesConfig(env, "", ""))

	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	for profile, dir := range map[string]string{"claude-personal": personal, "claude-work": work} {
		if got := hookCommands(t, env, filepath.Join(dir, "settings.json"))["Stop"]; !reflect.DeepEqual(got, []string{hookCommand(env, profile)}) {
			t.Errorf("%s: hooks.Stop = %v", profile, got)
		}
	}
}

func TestHooksSync_WarnsWhenTheBinaryTheHooksCallIsMissingOrNotExecutable(t *testing.T) {
	binary := func(env *batontest.Env) string { return env.Path(".baton", "bin", "baton") }
	tests := []struct {
		name  string
		setup func(env *batontest.Env)
		warns bool
	}{
		{"missing", func(env *batontest.Env) {}, true},
		{"not executable", func(env *batontest.Env) { env.WriteFile(".baton/bin/baton", "#!/bin/sh\n") }, true},
		{"a directory", func(env *batontest.Env) { env.MakeProfileDir(".baton/bin/baton") }, true},
		{"installed", func(env *batontest.Env) { env.WriteFile(".baton/bin/baton", "#!/bin/sh\n", true) }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t)
			env.MakeProfileDir(".claude-personal")
			env.WriteConfig(standardConfig(env))
			tt.setup(env)

			res := env.Run("sync")

			if res.ExitCode != 0 {
				t.Fatalf("exit code = %d, stderr=%q, want sync to succeed", res.ExitCode, res.Stderr)
			}
			warned := strings.Contains(res.Stderr, binary(env)) && strings.Contains(res.Stderr, "make install")
			if warned != tt.warns {
				t.Errorf("warned = %v, want %v; stderr=%q", warned, tt.warns, res.Stderr)
			}
			if tt.warns && !strings.Contains(res.Stderr, "warning") {
				t.Errorf("stderr = %q, want it to be a warning", res.Stderr)
			}
		})
	}
}

func TestHooksSync_WarnsOnEverySyncWhileTheBinaryIsMissing(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	env.WriteFile(".baton/bin/baton", "#!/bin/sh\n", true)
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatal(res.Stderr)
	}
	if err := os.Remove(env.Path(".baton/bin/baton")); err != nil {
		t.Fatal(err)
	}

	// The hooks are in place and unchanged, but their binary is gone: the
	// warning is still due, because every hook fails until it is back.
	res := env.Run("sync")

	if !strings.Contains(res.Stderr, "make install") {
		t.Errorf("stderr = %q, want the warning on every sync while the binary is missing", res.Stderr)
	}
}

const (
	restartNote      = "keep their old hooks and must be restarted"
	claudeLiveNote   = "status hooks installed; Claude Sessions of this Profile that are already running pick them up without a restart"
	codexRestartNote = "status hooks installed; Sessions of this Profile that are already running keep their old hooks and must be restarted to report Events; Codex runs hooks only after you review and trust them with /hooks"
)

func TestHooksSync_SaysOnceThatRunningClaudeSessionsPickUpHooksWhenTheyAreFirstAdded(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.MakeProfileDir(".claude-work")
	env.WriteConfig(twoProfilesConfig(env, "", ""))

	first := env.Run("sync")

	if first.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", first.ExitCode, first.Stderr)
	}
	for _, profile := range []string{"claude-personal", "claude-work"} {
		if !strings.Contains(first.Stdout, profile+": status hooks installed") {
			t.Errorf("stdout lacks the note for %s:\n%s", profile, first.Stdout)
		}
	}
	if got := strings.Count(first.Stdout, claudeLiveNote); got != 2 {
		t.Errorf("the note appears %d times, want once per Profile:\n%s", got, first.Stdout)
	}
	if strings.Contains(first.Stdout, restartNote) {
		t.Errorf("a Claude Profile is told to restart:\n%s", first.Stdout)
	}

	second := env.Run("sync")

	if strings.Contains(second.Stdout, restartNote) || strings.Contains(second.Stdout, "status hooks installed") {
		t.Errorf("a later sync repeats the note:\n%s", second.Stdout)
	}
}

func TestHooksSync_TheHooksNoteComesAgainOnlyForAProfileThatGetsHooksForTheFirstTime(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatal(res.Stderr)
	}
	env.MakeProfileDir(".claude-work")
	env.WriteConfig(twoProfilesConfig(env, "", ""))

	res := env.Run("sync")

	if !strings.Contains(res.Stdout, "claude-work: status hooks installed") || strings.Contains(res.Stdout, "claude-personal: status hooks installed") {
		t.Errorf("stdout =\n%s\nwant the note for claude-work only", res.Stdout)
	}
}

func TestHooksSync_TheHooksNoteIsGivenForProfilesInSandboxAndComposeToo(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(composeConfig(env, ""))
	sandboxProject(t, env, "box")
	composeProject(t, env, "app")
	devContainer(env, true)

	first := env.Run("sync")

	if first.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", first.ExitCode, first.Stderr)
	}
	for _, profile := range []string{"claude-personal", "box.claude", "app.claude"} {
		if want := profile + ": " + claudeLiveNote; !strings.Contains(first.Stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, first.Stdout)
		}
	}
	if want := "box.codex: " + codexRestartNote; !strings.Contains(first.Stdout, want) {
		t.Errorf("stdout lacks %q:\n%s", want, first.Stdout)
	}

	second := env.Run("sync")

	if strings.Contains(second.Stdout, "status hooks installed") {
		t.Errorf("a later sync repeats the note:\n%s", second.Stdout)
	}
}
