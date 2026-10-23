package test

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/loadkpi/baton/internal/batontest"
)

// ago is the time that was the given number of days (and hours) before now, as
// an Event carries it.
func ago(days, hours int) string {
	return time.Now().UTC().Add(-time.Duration(days)*24*time.Hour - time.Duration(hours)*time.Hour).Format(time.RFC3339Nano)
}

// hostPath and appPath are the Event files, relative to HOME, of a host
// Session and of a Session in the directory of Project "app".
func hostPath(sessionID string) string {
	return ".baton/sessions/claude-" + sessionID + ".jsonl"
}

func appPath(sessionID string) string {
	return ".baton/projects/app/sessions/claude-" + sessionID + ".jsonl"
}

// endedHost writes a host Session that ended `days` days ago.
func endedHost(t *testing.T, env *batontest.Env, repo, sessionID string, days int) {
	t.Helper()
	hostEvent(t, env, sessionID, ago(days, 1), "SessionStart", repo, proc{})
	hostEvent(t, env, sessionID, ago(days, 0), "SessionEnd", repo, proc{})
}

// pruneRows is the lines of the table `baton prune` prints, as cells: the
// header is dropped, and so is the line with the total.
func pruneRows(t *testing.T, stdout string) [][]string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) < 3 || strings.Fields(lines[0])[0] != "PROJECT" {
		t.Fatalf("prune output has no header and a total: %q", stdout)
	}
	var rows [][]string
	for _, line := range lines[1 : len(lines)-1] {
		cells := strings.Fields(line)
		for i := range cells {
			cells[i] = stable(cells[i])
		}
		rows = append(rows, cells)
	}
	return rows
}

func TestPrune_NeedsAWholeNumberOfDaysAndDeletesNothingOtherwise(t *testing.T) {
	cases := map[string][]string{
		"no flag":       {"prune"},
		"zero":          {"prune", "--older-than", "0d"},
		"bare zero":     {"prune", "--older-than", "0"},
		"negative":      {"prune", "--older-than", "-5d"},
		"fraction":      {"prune", "--older-than", "1.5d"},
		"hours":         {"prune", "--older-than", "90h"},
		"words":         {"prune", "--older-than", "soon"},
		"empty":         {"prune", "--older-than", ""},
		"flag no value": {"prune", "--older-than"},
		"extra word":    {"prune", "--older-than", "90d", "now"},
		"unknown flag":  {"prune", "--older-than", "90d", "--all"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			env := newEnv(t)
			repo := newRepo(t, env, "my-app")
			endedHost(t, env, repo, "old", 400)

			res := env.Run(args...)

			if res.ExitCode != 2 || res.Stdout != "" || !strings.Contains(res.Stderr, "usage: baton prune --older-than <N>d [--dry-run]") {
				t.Errorf("exit code = %d, stdout=%q, stderr=%q, want a usage error", res.ExitCode, res.Stdout, res.Stderr)
			}
			if !env.Exists(hostPath("old")) {
				t.Error("the Event file was deleted")
			}
		})
	}
}

func TestPrune_AcceptsTheDaysWithOrWithoutTheD(t *testing.T) {
	for _, arg := range []string{"90d", "90"} {
		t.Run(arg, func(t *testing.T) {
			env := newEnv(t)
			repo := newRepo(t, env, "my-app")
			endedHost(t, env, repo, "old", 100)
			endedHost(t, env, repo, "young", 80)

			res := env.Run("prune", "--older-than", arg)

			if res.ExitCode != 0 {
				t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
			}
			if env.Exists(hostPath("old")) || !env.Exists(hostPath("young")) {
				t.Errorf("old exists = %v, young exists = %v, want the 100 days old one gone and the 80 days old one kept",
					env.Exists(hostPath("old")), env.Exists(hostPath("young")))
			}
		})
	}
}

func TestPrune_DeletesTheEventFilesOfEndedAndLostSessionsOlderThanTheLimitAndNothingElse(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	livePID, liveStart := livePID(t)
	live := proc{host: thisHost(t), pid: livePID, start: liveStart}
	gone := proc{host: thisHost(t), pid: deadPID(t), start: 4242}

	endedHost(t, env, repo, "end-old", 200)
	endedHost(t, env, repo, "end-yng", 20)
	hostEvent(t, env, "lost-old", ago(150, 0), "PreToolUse", repo, gone)
	hostEvent(t, env, "lost-yng", ago(10, 0), "PreToolUse", repo, gone)
	hostEvent(t, env, "run-old", ago(300, 0), "PreToolUse", repo, live)
	hostEvent(t, env, "apr-old", ago(300, 0), "PermissionRequest", repo, live)
	hostEvent(t, env, "inp-old", ago(300, 0), "Stop", repo, live)
	hostEvent(t, env, "unk-old", ago(300, 0), "Whatever", repo, proc{})
	// Sessions in a Project's directory, as a sandbox writes them.
	writeProcEvent(t, env, appPath("sbx-old"), "sandbox", "app.claude", "sbx-old", ago(120, 1), "SessionStart", "/workspace", proc{})
	writeProcEvent(t, env, appPath("sbx-old"), "sandbox", "app.claude", "sbx-old", ago(120, 0), "SessionEnd", "/workspace", proc{})
	writeProcEvent(t, env, appPath("sbx-yng"), "sandbox", "app.claude", "sbx-yng", ago(5, 0), "SessionEnd", "/workspace", proc{})
	// What belongs to the Project and must outlive its Events.
	env.WriteFile(".baton/projects/app/handoffs/fix/1.md", "# Handoff\nEvents: "+env.Path(appPath("sbx-old"))+"\n")
	env.WriteFile(".baton/projects/app/current.jsonl", "{\"v\":1}\n")
	lsBefore := lsRows(t, env) // registers Project my-app: .baton/projects/my-app/project.toml
	sizes := fileSizes(t, env, hostPath("end-old"), hostPath("lost-old"), appPath("sbx-old"))

	res := env.Run("prune", "--older-than", "90d")

	if res.ExitCode != 0 || res.Stderr != "" {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	for _, gone := range []string{hostPath("end-old"), hostPath("lost-old"), appPath("sbx-old")} {
		if env.Exists(gone) {
			t.Errorf("%s was not deleted", gone)
		}
	}
	for _, id := range []string{"end-yng", "lost-yng", "run-old", "apr-old", "inp-old", "unk-old"} {
		if !env.Exists(hostPath(id)) {
			t.Errorf("%s was deleted", id)
		}
	}
	for _, kept := range []string{
		appPath("sbx-yng"), ".baton/projects/app/current.jsonl", ".baton/projects/my-app/project.toml",
		".baton/projects/app/handoffs/fix/1.md", ".baton/projects/app/sessions",
	} {
		if !env.Exists(kept) {
			t.Errorf("%s is gone", kept)
		}
	}

	// Each deleted file is listed with the Session as baton ls named it, and
	// the status it had.
	rows := pruneRows(t, res.Stdout)
	wantFiles := map[string]string{
		"end-old":  env.Path(hostPath("end-old")),
		"lost-old": env.Path(hostPath("lost-old")),
		"sbx-old":  env.Path(appPath("sbx-old")),
	}
	if len(rows) != len(wantFiles) {
		t.Fatalf("%d rows, want %d: %v", len(rows), len(wantFiles), rows)
	}
	for _, r := range rows {
		if len(r) != 8 {
			t.Fatalf("row %q has %d cells, want PROJECT TASK AGENT SESSION PROFILE RUNTIME STATUS FILE", r, len(r))
		}
		if r[7] != wantFiles[r[3]] {
			t.Errorf("row %q: file = %q, want %q", r, r[7], wantFiles[r[3]])
		}
		var listed []string
		for _, l := range lsBefore {
			if l[3] == r[3] {
				listed = l
			}
		}
		if strings.Join(r[:7], " ") != strings.Join(listed, " ") {
			t.Errorf("row %q does not name the Session as baton ls did: %q", r[:7], listed)
		}
	}
	statuses := map[string]string{}
	for _, r := range rows {
		statuses[r[3]] = r[6]
	}
	if statuses["end-old"] != "ended" || statuses["lost-old"] != "lost" || statuses["sbx-old"] != "ended" {
		t.Errorf("statuses = %v", statuses)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	wantTotal := "deleted 3 files, " + strconv.Itoa(sizes) + " bytes"
	if last := lines[len(lines)-1]; last != wantTotal {
		t.Errorf("total line = %q, want %q", last, wantTotal)
	}

	// Everything else works as before.
	var wantLs [][]string
	for _, l := range lsBefore {
		if l[3] != "end-old" && l[3] != "lost-old" && l[3] != "sbx-old" {
			wantLs = append(wantLs, l)
		}
	}
	if got := lsRows(t, env); !reflect.DeepEqual(got, wantLs) {
		t.Errorf("ls after the prune = %v, want %v", got, wantLs)
	}
}

// fileSizes is the total size in bytes of the files at the paths (relative to
// HOME).
func fileSizes(t *testing.T, env *batontest.Env, paths ...string) int {
	t.Helper()
	total := 0
	for _, p := range paths {
		info, err := os.Stat(env.Path(p))
		if err != nil {
			t.Fatal(err)
		}
		total += int(info.Size())
	}
	return total
}

func TestPrune_DryRunListsWhatItWouldDeleteAndDeletesNothing(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	endedHost(t, env, repo, "end-old", 200)
	endedHost(t, env, repo, "end-yng", 20)
	before := env.ReadFile(hostPath("end-old"))
	size := fileSizes(t, env, hostPath("end-old"))

	res := env.Run("prune", "--older-than", "90d", "--dry-run")

	if res.ExitCode != 0 || res.Stderr != "" {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	rows := pruneRows(t, res.Stdout)
	if len(rows) != 1 || rows[0][3] != "end-old" || rows[0][7] != env.Path(hostPath("end-old")) {
		t.Errorf("rows = %v, want the one old Session and its file", rows)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if want := "would delete 1 file, " + strconv.Itoa(size) + " bytes"; lines[len(lines)-1] != want {
		t.Errorf("total line = %q, want %q", lines[len(lines)-1], want)
	}
	if env.ReadFile(hostPath("end-old")) != before || !env.Exists(hostPath("end-yng")) {
		t.Error("--dry-run changed the Event files")
	}
}

func TestPrune_SaysSoWhenThereIsNothingToPrune(t *testing.T) {
	env := newEnv(t)

	empty := env.Run("prune", "--older-than", "90d")

	repo := newRepo(t, env, "my-app")
	endedHost(t, env, repo, "end-yng", 20)
	young := env.Run("prune", "--older-than", "90d", "--dry-run")

	for name, res := range map[string]batontest.Result{"no sessions": empty, "only young ones": young} {
		if res.ExitCode != 0 || res.Stdout != "nothing to prune\n" || res.Stderr != "" {
			t.Errorf("%s: exit code = %d, stdout=%q, stderr=%q, want %q", name, res.ExitCode, res.Stdout, res.Stderr, "nothing to prune\n")
		}
	}
}

func TestPrune_JudgesAgeByTheNewestEventOfTheFileWhoeverWroteIt(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	// Ended long ago, but an Event the status table ignores came after.
	endedHost(t, env, repo, "late-ev", 200)
	hostEvent(t, env, "late-ev", ago(1, 0), "Whatever", repo, proc{})
	// The Agent's last Event is old; Baton wrote the end of the Session lately.
	hostEvent(t, env, "bat-new", ago(200, 0), "Stop", repo, proc{})
	writeBatonEnd(t, env, "bat-new", ago(1, 0), repo)
	// Baton wrote the end long ago too.
	hostEvent(t, env, "bat-old", ago(200, 0), "Stop", repo, proc{})
	writeBatonEnd(t, env, "bat-old", ago(150, 0), repo)

	res := env.Run("prune", "--older-than", "90d")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if !env.Exists(hostPath("late-ev")) || !env.Exists(hostPath("bat-new")) {
		t.Error("a Session with a young Event was deleted")
	}
	if env.Exists(hostPath("bat-old")) {
		t.Error("a Session whose newest Event, Baton's, is old was kept")
	}
}

// writeBatonEnd appends the Event `baton session end` writes, at the given
// time.
func writeBatonEnd(t *testing.T, env *batontest.Env, sessionID, ts, cwd string) {
	t.Helper()
	line := `{"v":1,"ts":"` + ts + `","agent":"claude","profile":"claude-personal","runtime":"host","session_id":"` + sessionID +
		`","event":"SessionEnd","cwd":"` + cwd + `","payload":{"reason":"ended by hand"},"source":"baton"}` + "\n"
	env.WriteFile(hostPath(sessionID), env.ReadFile(hostPath(sessionID))+line)
}

func TestPrune_ReportsAFileItCannotDeleteAndStillProcessesTheOthers(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	endedHost(t, env, repo, "end-old", 200)
	writeProcEvent(t, env, appPath("sbx-old"), "sandbox", "app.claude", "sbx-old", ago(120, 0), "SessionEnd", "/workspace", proc{})
	hostDir := env.Path(".baton", "sessions")
	if err := os.Chmod(hostDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(hostDir, 0o700) })
	if f, err := os.Create(filepath.Join(hostDir, "probe")); err == nil {
		_ = f.Close()
		t.Skip("this user can write into a read-only directory (root?), so a file cannot be made undeletable")
	}

	res := env.Run("prune", "--older-than", "90d")

	if res.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, env.Path(hostPath("end-old"))) || !strings.Contains(res.Stderr, "permission denied") {
		t.Errorf("stderr = %q, want the file and the reason", res.Stderr)
	}
	if !env.Exists(hostPath("end-old")) {
		t.Error("the file in the read-only directory is gone")
	}
	if env.Exists(appPath("sbx-old")) {
		t.Error("the other file was not processed")
	}
	rows := pruneRows(t, res.Stdout)
	if len(rows) != 1 || rows[0][3] != "sbx-old" {
		t.Errorf("rows = %v, want only the file that was deleted", rows)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if !strings.HasPrefix(lines[len(lines)-1], "deleted 1 file, ") {
		t.Errorf("total line = %q, want it to count the one deleted file", lines[len(lines)-1])
	}
}

func TestPrune_ASessionThatComesBackAfterItsFileWasPrunedStartsANewFile(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	endedHost(t, env, repo, "end-old", 200)
	if res := env.Run("prune", "--older-than", "90d"); res.ExitCode != 0 {
		t.Fatalf("prune: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	hook := runHook(t, env, "claude-personal", "UserPromptSubmit", "end-old", repo)

	if hook.ExitCode != 0 {
		t.Fatalf("hook: exit code = %d, stderr=%q", hook.ExitCode, hook.Stderr)
	}
	if lines := eventLines(t, env, hostPath("end-old")); len(lines) != 1 || lines[0]["event"] != "UserPromptSubmit" {
		t.Errorf("lines = %v, want a new file with the one Event", lines)
	}
	rows := lsRows(t, env)
	if len(rows) != 1 || rows[0][3] != "end-old" || rows[0][6] != "running" {
		t.Errorf("rows = %v, want the Session listed again, running", rows)
	}
}

// lockFile takes the lock a hook takes on the file at path (relative to HOME),
// the way a hook that is in the middle of its append holds it. The lock is
// released by the returned function.
func lockFile(t *testing.T, env *batontest.Env, path string) (release func()) {
	t.Helper()
	f, err := os.Open(env.Path(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release = func() { once.Do(func() { _ = f.Close() }) }
	t.Cleanup(release)
	return release
}

// waitForLockWaiter waits until a process is blocked on a lock of the file at
// path (relative to HOME), by what the kernel lists in /proc/locks.
func waitForLockWaiter(t *testing.T, env *batontest.Env, path string) {
	t.Helper()
	info, err := os.Stat(env.Path(path))
	if err != nil {
		t.Fatal(err)
	}
	ino := info.Sys().(*syscall.Stat_t).Ino
	suffix := ":" + strconv.FormatUint(ino, 10) + " "
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile("/proc/locks")
		if err != nil {
			t.Skipf("cannot tell who waits for a lock: %v", err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "->") && strings.Contains(line, suffix) {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no process waits for the lock on " + path)
}

func TestPrune_ChecksTheFileAgainUnderTheLockAndKeepsOneThatGotANewEventMeanwhile(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	endedHost(t, env, repo, "end-old", 200)
	release := lockFile(t, env, hostPath("end-old"))

	prune := env.Start("prune", "--older-than", "90d")
	waitForLockWaiter(t, env, hostPath("end-old"))
	// A hook lands its line while the prune waits for the lock.
	hostEvent(t, env, "end-old", time.Now().UTC().Format(time.RFC3339Nano), "UserPromptSubmit", repo, proc{})
	release()
	res := prune.Wait()

	if res.ExitCode != 0 {
		t.Errorf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	lines := eventLines(t, env, hostPath("end-old"))
	if len(lines) != 3 || lines[2]["event"] != "UserPromptSubmit" {
		t.Errorf("lines = %v, want the Session's file with the line the hook wrote", lines)
	}
	if res.Stdout != "nothing to prune\n" {
		t.Errorf("stdout = %q, want %q: the file was not deleted", res.Stdout, "nothing to prune\n")
	}
}

func TestHook_AHookWaitingForTheLockWhenItsFileIsDeletedStillWritesItsLine(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	endedHost(t, env, repo, "end-old", 200)
	release := lockFile(t, env, hostPath("end-old"))
	stdin := payload(t, "UserPromptSubmit", "end-old", repo)

	done := make(chan batontest.Result, 1)
	go func() { done <- env.RunWithStdin(stdin, hookArgs("claude-personal")...) }()
	waitForLockWaiter(t, env, hostPath("end-old"))
	// The prune of that moment: it deletes the file and lets go of the lock.
	if err := os.Remove(env.Path(hostPath("end-old"))); err != nil {
		t.Fatal(err)
	}
	release()
	res := <-done

	if res.ExitCode != 0 {
		t.Fatalf("hook: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if !env.Exists(hostPath("end-old")) {
		t.Fatal("the hook's line went into a file that is gone")
	}
	if lines := eventLines(t, env, hostPath("end-old")); len(lines) != 1 || lines[0]["event"] != "UserPromptSubmit" {
		t.Errorf("lines = %v, want a new file with the hook's line", lines)
	}
}
