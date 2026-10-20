package test

import (
	"encoding/json"
	"github.com/loadkpi/baton/internal/batontest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestCodexHook_RecordsTheEventWithTheFieldsClaudeUses(t *testing.T) {
	env := newEnv(t)
	cwd := env.Path("work", "app")

	res := env.RunWithStdin(fixture(t, "codex", "Stop", "thr_123", cwd),
		"hook", "codex", "--profile", "app.codex", "--runtime", "host")

	if res.ExitCode != 0 || res.Stdout != "" || res.Stderr != "" {
		t.Fatalf("exit code = %d, stdout=%q, stderr=%q, want a silent success", res.ExitCode, res.Stdout, res.Stderr)
	}
	lines := eventLines(t, env, ".baton/sessions/codex-thr_123.jsonl")
	if len(lines) != 1 {
		t.Fatalf("%d lines, want 1", len(lines))
	}
	for key, want := range map[string]any{
		"agent": "codex", "profile": "app.codex", "session_id": "thr_123", "event": "Stop", "cwd": cwd,
	} {
		if !reflect.DeepEqual(lines[0][key], want) {
			t.Errorf("%s = %#v, want %#v", key, lines[0][key], want)
		}
	}
	payload, _ := lines[0]["payload"].(map[string]any)
	if payload["turn_id"] != "turn_1" || payload["last_assistant_message"] != "The test passes now." {
		t.Errorf("payload = %v, want the Codex payload as it came in", payload)
	}
}

// codexHook plays Codex in the sandbox of Project app calling its hook for one
// Event.
func codexHook(t *testing.T, env *batontest.Env, root, event, sessionID, cwd string) batontest.Result {
	t.Helper()
	res := env.RunWithEnv([]string{"BATON_CONTAINER_ROOT=" + root}, fixture(t, "codex", event, sessionID, cwd),
		"hook", "codex", "--profile", "app.codex", "--runtime", "sandbox")
	if res.ExitCode != 0 || res.Stdout != "" || res.Stderr != "" {
		t.Fatalf("hook %s: exit code = %d, stdout=%q, stderr=%q, want a silent success", event, res.ExitCode, res.Stdout, res.Stderr)
	}
	return res
}

func TestCodexNew_StatusesFollowTheCodexColumnOfTheStatusTable(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	if res := env.Run("new", "app", "--agent", "codex"); res.ExitCode != 0 {
		t.Fatalf("new: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	root := containerRoot(t, env, "app")

	steps := []struct{ event, want string }{
		{"SessionStart", "waiting_input"},
		{"UserPromptSubmit", "running"},
		{"PreToolUse", "running"},
		{"PermissionRequest", "waiting_approval"},
		{"PostToolUse", "running"},
		{"Interrupt", "waiting_input"},
		{"UserPromptSubmit", "running"},
		{"Stop", "waiting_input"},
		{"SessionEnd", "ended"},
	}
	for _, step := range steps {
		codexHook(t, env, root, step.event, "thr_123", "/workspace")
		want := [][]string{{"app", "YYYY-MM-DD-1", "codex", "thr_123", "app.codex", "sandbox", step.want}}
		if got := lsRows(t, env); !reflect.DeepEqual(got, want) {
			t.Fatalf("after %s: rows = %v, want %v", step.event, got, want)
		}
	}
}

func TestCodexNew_RunsCodexInTheContainerWithoutAPIKeysUnderATabTitledWithTheAgent(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)

	res := env.Run("new", "app", "login-fix", "--agent", "codex")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if title := guakeArgs(env)[0][3]; title != "app/login-fix:codex" {
		t.Errorf("tab title = %q, want app/login-fix:codex", title)
	}
	args := runTabLine(t, env)
	wantTail := []string{imageTag, "env", "-u", "ANTHROPIC_API_KEY", "-u", "OPENAI_API_KEY", "codex"}
	if got := args[len(args)-len(wantTail):]; strings.Join(got, " ") != strings.Join(wantTail, " ") {
		t.Errorf("end of the docker command = %q, want %q", got, wantTail)
	}
}

func TestCodexNew_ARealCodexIsNeverStarted(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)

	env.Run("new", "app", "--agent", "codex")
	runTabLine(t, env)

	if calls := env.Calls("codex"); len(calls) != 0 {
		t.Errorf("codex stub calls = %v, want none: Codex runs inside docker, which is a stub", calls)
	}
}

func TestCodexNew_CodexRunsOnlyInASandboxSoAHostProjectIsRefused(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")

	res := env.Run("new", repo, "--agent", "codex")

	want := "baton: project my-app runs in the host Runtime; codex runs only in a sandbox (a Project with .ai-sandbox/run.sh)\n"
	if res.ExitCode != 1 || res.Stderr != want {
		t.Errorf("exit code = %d, stderr=%q, want 1 and %q", res.ExitCode, res.Stderr, want)
	}
	if calls := env.Calls("guake"); len(calls) != 0 {
		t.Errorf("guake calls = %v, want none", calls)
	}
}

func TestCodexConfig_AHostProfileOfCodexIsRefused(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".codex")
	env.WriteConfig("source = \"" + env.SourceDir + "\"\n[profiles.c]\nagent = \"codex\"\ndir = \"" + env.Path(".codex") + "\"\n")

	res := env.Run("sync")

	if res.ExitCode == 0 || !strings.Contains(res.Stderr, `agent "codex" runs only in a sandbox`) {
		t.Errorf("exit code = %d, stderr=%q, want a refusal that says the agent runs only in a sandbox", res.ExitCode, res.Stderr)
	}
}

// codexHookBudget is far above what one append takes and far below Codex's
// 1-second SessionEnd budget, so that a loaded machine does not make the test
// flaky while a hook that starts doing real work (git, a network call) still
// fails it.
const codexHookBudget = 300 * time.Millisecond

func TestCodexHook_OneAppendFitsFarInsideTheSessionEndBudget(t *testing.T) {
	env := newEnv(t)
	cwd := env.Path("work", "app")
	const runs = 7
	var took []time.Duration
	for i := 0; i < runs; i++ {
		input := fixture(t, "codex", "SessionEnd", "thr_123", cwd)
		start := time.Now()
		res := env.RunWithStdin(input, "hook", "codex", "--profile", "app.codex", "--runtime", "host")
		took = append(took, time.Since(start))
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
		}
	}
	sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
	median := took[runs/2]
	t.Logf("baton hook codex, process start to exit, %d runs: min %v, median %v, max %v", runs, took[0], median, took[runs-1])
	if median > codexHookBudget {
		t.Errorf("median of one append = %v, want under %v (Codex gives SessionEnd 1 second)", median, codexHookBudget)
	}
	if got := len(eventLines(t, env, ".baton/sessions/codex-thr_123.jsonl")); got != runs {
		t.Errorf("%d lines, want %d", got, runs)
	}
}

// hintLines are the lines of `baton ls` output that point to /hooks.
func hintLines(stdout string) []string {
	var out []string
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, "/hooks") {
			out = append(out, line)
		}
	}
	return out
}

const hooksHint = "app/YYYY-MM-DD-1:codex has reported no events. Codex runs Baton's status hooks only after you trust them: run /hooks in that session."

func TestCodexLs_ASessionBatonStartedThatReportsNothingIsFlaggedWithAHintToTrustTheHooks(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	if res := env.Run("new", "app", "--agent", "codex"); res.ExitCode != 0 {
		t.Fatalf("new: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	res := env.Run("ls")

	if res.ExitCode != 0 {
		t.Fatalf("ls: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	rows := lsRows(t, env)
	if want := []string{"app", "YYYY-MM-DD-1", "codex", "-", "app.codex", "sandbox", "no_events"}; !reflect.DeepEqual(rows[0], want) {
		t.Errorf("first row = %v, want %v", rows[0], want)
	}
	hints := hintLines(res.Stdout)
	if len(hints) != 1 || stable(hints[0]) != hooksHint {
		t.Errorf("hints = %q, want exactly %q", hints, hooksHint)
	}
}

func TestCodexLs_TheHintGoesAwayWithTheFirstEvent(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	env.Run("new", "app", "--agent", "codex")
	root := containerRoot(t, env, "app")

	codexHook(t, env, root, "SessionStart", "thr_123", "/workspace")

	res := env.Run("ls")
	if hints := hintLines(res.Stdout); len(hints) != 0 {
		t.Errorf("hints = %q, want none once an Event arrived", hints)
	}
	if rows := lsRows(t, env); !reflect.DeepEqual(rows, [][]string{{"app", "YYYY-MM-DD-1", "codex", "thr_123", "app.codex", "sandbox", "waiting_input"}}) {
		t.Errorf("rows = %v, want the Session with its status", rows)
	}
}

func TestCodexLs_EventsOfAnEarlierSessionDoNotHideALaunchThatReportsNothing(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	env.Run("new", "app", "--agent", "codex")
	root := containerRoot(t, env, "app")
	codexHook(t, env, root, "SessionStart", "thr_old", "/workspace")
	codexHook(t, env, root, "SessionEnd", "thr_old", "/workspace")
	if res := env.Run("new", "app", "--agent", "codex"); res.ExitCode != 0 {
		t.Fatalf("second new: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	res := env.Run("ls")

	if hints := hintLines(res.Stdout); len(hints) != 1 {
		t.Errorf("hints = %q, want one for the second launch", hints)
	}
	var statuses []string
	for _, row := range lsRows(t, env) {
		statuses = append(statuses, row[len(row)-1])
	}
	sort.Strings(statuses)
	if want := []string{"ended", "no_events"}; !reflect.DeepEqual(statuses, want) {
		t.Errorf("statuses = %v, want %v", statuses, want)
	}
}

func TestCodexLs_OnlyTheLatestLaunchOfAProjectIsFlagged(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	env.Run("new", "app", "first", "--agent", "codex")
	env.Run("new", "app", "second", "--agent", "codex")

	res := env.Run("ls")

	if hints := hintLines(res.Stdout); len(hints) != 1 || !strings.HasPrefix(hints[0], "app/second:codex ") {
		t.Errorf("hints = %q, want one, for app/second:codex", hints)
	}
	if rows := lsRows(t, env); len(rows) != 1 {
		t.Errorf("rows = %v, want one", rows)
	}
}

func TestCodexLs_AClaudeLaunchIsNotFlaggedAndALaunchWithoutATabLeavesNoMarker(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	env.Run("new", "app", "--agent", "claude")
	env.Stub("guake", batontest.WithScript("exit 1"))
	if res := env.Run("new", "app", "--agent", "codex"); res.ExitCode == 0 {
		t.Fatal("new succeeded although guake failed")
	}

	res := env.Run("ls")

	if res.Stdout != "no sessions\n" {
		t.Errorf("ls = %q, want no sessions: nothing is waiting for a Codex that was never started", res.Stdout)
	}
}

func TestCodexNew_TheLaunchMarkerRecordsAgentProfileTimeAndTabTitle(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	before := time.Now().Add(-time.Second)

	env.Run("new", "app", "login-fix", "--agent", "codex")

	if got := env.ListDir(".baton/projects/app/launches"); !reflect.DeepEqual(got, []string{"codex.json"}) {
		t.Fatalf("launches = %v, want codex.json", got)
	}
	var marker map[string]any
	if err := json.Unmarshal([]byte(env.ReadFile(".baton/projects/app/launches/codex.json")), &marker); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{"v": float64(1), "agent": "codex", "profile": "app.codex", "runtime": "sandbox", "title": "app/login-fix:codex", "task": "login-fix"} {
		if marker[key] != want {
			t.Errorf("%s = %#v, want %#v", key, marker[key], want)
		}
	}
	ts, err := time.Parse(time.RFC3339Nano, marker["ts"].(string))
	if err != nil || ts.Before(before) || ts.After(time.Now().Add(time.Second)) {
		t.Errorf("ts = %v (%v), want now", marker["ts"], err)
	}
	if mode := env.Mode(".baton/projects/app/launches/codex.json"); mode != 0o600 {
		t.Errorf("marker mode = %04o, want 0600", mode)
	}
}

func TestCodexNew_BatonNeverWritesCodexHookTrustState(t *testing.T) {
	env := newEnv(t)
	_, home := launchProject(t, env)
	syncOK(t, env)
	if res := env.Run("new", "app", "--agent", "codex"); res.ExitCode != 0 {
		t.Fatalf("new: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	codex := filepath.Join(home, ".codex")
	t.Logf("entries of the Codex Profile: %v", env.ListDir(codex))
	// Trust is recorded against the hash of each hook, in Codex's own state
	// (its location is [unverified]); Baton writes the hook definitions and
	// nothing that looks like a review.
	for _, rel := range env.ListDir(codex) {
		if strings.Contains(strings.ToLower(rel), "trust") {
			t.Errorf("%s exists in the Codex Profile, want no trust state written by Baton", rel)
		}
	}
	for _, file := range []string{"config.toml", "hooks.json"} {
		text := strings.ToLower(env.ReadFile(filepath.Join(codex, file)))
		for _, word := range []string{"trust", "hash"} {
			if strings.Contains(text, word) {
				t.Errorf("%s contains %q, want no trust state written by Baton", file, word)
			}
		}
	}
}

func TestCodexHook_TheCommandSyncWroteRecordsTheEventWhenTheBinaryIsThere(t *testing.T) {
	env := newEnv(t)
	_, home := launchProject(t, env)
	root := containerRoot(t, env, "app")
	command := writtenSandboxHook(t, env, home, "codex", batonBin)

	res := env.RunProgram([]string{"BATON_CONTAINER_ROOT=" + root}, fixture(t, "codex", "Stop", "thr_123", "/workspace"), "sh", "-c", command)

	if res.ExitCode != 0 || res.Stdout != "" || res.Stderr != "" {
		t.Fatalf("exit code = %d, stdout=%q, stderr=%q, want a silent success", res.ExitCode, res.Stdout, res.Stderr)
	}
	lines := eventLines(t, env, ".baton/projects/app/sessions/codex-thr_123.jsonl")
	if len(lines) != 1 || lines[0]["event"] != "Stop" || lines[0]["profile"] != "app.codex" || lines[0]["runtime"] != "sandbox" {
		t.Errorf("lines = %v, want one sandbox Stop Event of app.codex", lines)
	}
}
