package test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/loadkpi/baton/internal/batontest"
)

const appLaunch = ".baton/projects/app/launches/codex.json"

// writeCodexLaunch writes the launch record `baton new` leaves for Codex in
// Project app, as of the given time.
func writeCodexLaunch(t *testing.T, env *batontest.Env, ts string) {
	t.Helper()
	doc, err := json.Marshal(map[string]any{
		"v": 1, "ts": ts, "agent": "codex", "profile": "app.codex", "runtime": "sandbox",
		"task": "YYYY-MM-DD-1", "title": "app/YYYY-MM-DD-1:codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	env.WriteFile(appLaunch, string(doc)+"\n")
}

// writeCodexEvent appends an Event of a Codex Session to the file at path.
func writeCodexEvent(t *testing.T, env *batontest.Env, path, sessionID, ts, event string) {
	t.Helper()
	line := `{"v":1,"ts":"` + ts + `","agent":"codex","profile":"app.codex","runtime":"sandbox","session_id":"` + sessionID +
		`","event":"` + event + `","cwd":"/workspace","payload":{}}` + "\n"
	existing := ""
	if env.Exists(path) {
		existing = env.ReadFile(path)
	}
	env.WriteFile(path, existing+line)
}

func codexAppPath(sessionID string) string {
	return ".baton/projects/app/sessions/codex-" + sessionID + ".jsonl"
}

// endedCodex writes a Codex Session of Project app that ended `days` days ago.
func endedCodex(t *testing.T, env *batontest.Env, sessionID string, days int) {
	t.Helper()
	writeCodexEvent(t, env, codexAppPath(sessionID), sessionID, ago(days, 1), "SessionStart")
	writeCodexEvent(t, env, codexAppPath(sessionID), sessionID, ago(days, 0), "SessionEnd")
}

func lsStatuses(t *testing.T, env *batontest.Env) []string {
	t.Helper()
	var out []string
	for _, row := range lsRows(t, env) {
		out = append(out, row[len(row)-1])
	}
	return out
}

func TestPrune_ALaunchThatWasHeardFromDoesNotLookSilentAfterItsEventsAreGone(t *testing.T) {
	env := newEnv(t)
	writeCodexLaunch(t, env, ago(300, 0))
	endedCodex(t, env, "thr_old", 200)
	if res := env.Run("ls"); strings.Contains(res.Stdout, "/hooks") || strings.Contains(res.Stdout, "no_events") {
		t.Fatalf("ls before the prune = %q, want no hint: the launch was heard from", res.Stdout)
	}

	res := env.Run("prune", "--older-than", "90d")

	if res.ExitCode != 0 || res.Stderr != "" {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if env.Exists(codexAppPath("thr_old")) {
		t.Error("the Event file was not deleted")
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if last := lines[len(lines)-1]; last != "removed 1 launch record" {
		t.Errorf("last line = %q, want %q", last, "removed 1 launch record")
	}
	if env.Exists(appLaunch) {
		t.Error("the launch record is still there")
	}
	ls := env.Run("ls")
	if hints := hintLines(ls.Stdout); len(hints) != 0 || strings.Contains(ls.Stdout, "no_events") {
		t.Errorf("ls after the prune = %q, want no hint and no no_events row", ls.Stdout)
	}
}

func TestPrune_ALaunchThatNeverHeardFromAnythingStaysFlaggedAndOnDisk(t *testing.T) {
	env := newEnv(t)
	// The Events are from before the launch, so they never hid it.
	endedCodex(t, env, "thr_old", 200)
	writeCodexLaunch(t, env, ago(100, 0))
	if before := lsStatuses(t, env); len(before) != 2 {
		t.Fatalf("statuses before the prune = %v, want the ended Session and no_events", before)
	}

	res := env.Run("prune", "--older-than", "90d")

	if res.ExitCode != 0 || res.Stderr != "" {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if strings.Contains(res.Stdout, "launch record") {
		t.Errorf("stdout = %q, want no mention of launch records", res.Stdout)
	}
	if !env.Exists(appLaunch) {
		t.Error("the launch record of a silent launch was removed")
	}
	if got := lsStatuses(t, env); strings.Join(got, " ") != "no_events" {
		t.Errorf("statuses after the prune = %v, want only no_events", got)
	}
	if hints := hintLines(env.Run("ls").Stdout); len(hints) != 1 {
		t.Errorf("hints = %q, want the one for the silent launch", hints)
	}
}

func TestPrune_ALaunchHeardFromByASessionThatIsNotPrunedIsUntouched(t *testing.T) {
	env := newEnv(t)
	writeCodexLaunch(t, env, ago(300, 0))
	endedCodex(t, env, "thr_old", 200)
	endedCodex(t, env, "thr_yng", 20)

	res := env.Run("prune", "--older-than", "90d")

	if res.ExitCode != 0 || res.Stderr != "" {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if env.Exists(codexAppPath("thr_old")) || !env.Exists(codexAppPath("thr_yng")) {
		t.Error("the old file should be gone and the young one kept")
	}
	if strings.Contains(res.Stdout, "launch record") || !env.Exists(appLaunch) {
		t.Errorf("stdout = %q, launch exists = %v, want the launch untouched", res.Stdout, env.Exists(appLaunch))
	}
	if hints := hintLines(env.Run("ls").Stdout); len(hints) != 0 {
		t.Errorf("hints = %q, want none", hints)
	}
}

func TestPrune_DryRunKeepsTheLaunchRecordAndSaysNothingAboutIt(t *testing.T) {
	env := newEnv(t)
	writeCodexLaunch(t, env, ago(300, 0))
	endedCodex(t, env, "thr_old", 200)

	res := env.Run("prune", "--older-than", "90d", "--dry-run")

	if res.ExitCode != 0 || res.Stderr != "" {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if strings.Contains(res.Stdout, "launch record") {
		t.Errorf("stdout = %q, want no mention of launch records", res.Stdout)
	}
	if !env.Exists(appLaunch) || !env.Exists(codexAppPath("thr_old")) {
		t.Error("--dry-run deleted something")
	}
}

func TestPrune_HostSessionsNeverHideALaunchSoPruningThemLeavesItAlone(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	writeCodexLaunch(t, env, ago(300, 0))
	endedHost(t, env, repo, "old", 200)
	writeCodexEvent(t, env, ".baton/sessions/codex-host.jsonl", "host", ago(200, 1), "SessionEnd")

	res := env.Run("prune", "--older-than", "90d")

	if res.ExitCode != 0 || res.Stderr != "" {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if env.Exists(hostPath("old")) || env.Exists(".baton/sessions/codex-host.jsonl") {
		t.Error("the host files were not deleted")
	}
	if strings.Contains(res.Stdout, "launch record") || !env.Exists(appLaunch) {
		t.Errorf("stdout = %q, launch exists = %v, want the launch untouched", res.Stdout, env.Exists(appLaunch))
	}
}
