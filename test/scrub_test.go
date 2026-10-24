package test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/loadkpi/baton/internal/batontest"
)

// bashEvent is the line of a finished Bash command as an Event file written
// before masking existed held it.
func bashEvent(t *testing.T, env *batontest.Env, path, sessionID, command string) {
	t.Helper()
	writeProcEventPayload(t, env, path, "host", "claude-personal", sessionID, "2031-03-04T10:00:00Z", "PostToolUse", "/x", proc{},
		map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": command}})
	if err := os.Chmod(env.Path(path), 0o600); err != nil {
		t.Fatal(err)
	}
}

// chmod600 makes the file the way Baton writes its own.
func chmod600(t *testing.T, env *batontest.Env, path string) {
	t.Helper()
	if err := os.Chmod(env.Path(path), 0o600); err != nil {
		t.Fatal(err)
	}
}

// commandOf is the command of the n-th (from 0) line of an Event file.
func commandOf(t *testing.T, env *batontest.Env, path string, n int) string {
	t.Helper()
	line := eventLines(t, env, path)[n]
	return line["payload"].(map[string]any)["tool_input"].(map[string]any)["command"].(string)
}

func TestScrub_MasksOldEventsInTheHostAndProjectDirectoriesAndKeepsTheirMode(t *testing.T) {
	env := newEnv(t)
	writeSecrets(t, env, 0o600, "LITERAL=literalsecret1\n")
	host, app := hostPath("s-host"), appPath("s-app")
	bashEvent(t, env, host, "s-host", "curl -H 'Authorization: Bearer abc.DEF-123_456~xyz' literalsecret1 "+tokGitLab+" "+tokAnthropic)
	bashEvent(t, env, app, "s-app", "export API_TOKEN=hunter2hunter2 && echo "+tokJWT)

	res := env.Run("scrub")

	if res.ExitCode != 0 || res.Stderr != "" {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := env.Path(host) + ": 1 line\n" + env.Path(app) + ": 1 line\nscrubbed 2 files, 2 lines\n"
	if res.Stdout != want {
		t.Errorf("stdout = %q, want %q", res.Stdout, want)
	}
	if got, want := commandOf(t, env, host, 0), "curl -H 'Authorization: Bearer "+mask+"' "+mask+" "+mask+" "+mask; got != want {
		t.Errorf("host command = %q, want %q", got, want)
	}
	if got, want := commandOf(t, env, app, 0), "export API_TOKEN="+mask+" && echo "+mask; got != want {
		t.Errorf("project command = %q, want %q", got, want)
	}
	for _, path := range []string{host, app} {
		if event := eventLines(t, env, path)[0]; event["masked"] != true {
			t.Errorf("%s: masked = %v, want true", path, event["masked"])
		}
		if mode := env.Mode(path); mode != 0o600 {
			t.Errorf("%s: mode = %v, want 0600", path, mode)
		}
		for _, secret := range []string{"abc.DEF-123_456~xyz", "literalsecret1", tokGitLab, tokAnthropic, tokJWT, "hunter2hunter2"} {
			if strings.Contains(env.ReadFile(path), secret) {
				t.Errorf("%s still holds a secret: %s", path, secret)
			}
		}
	}
	if strings.Contains(res.Stdout+res.Stderr, "literalsecret1") {
		t.Error("the output holds a secret")
	}
}

func TestScrub_KeepsALineThatIsNotAnEventByteForByteAndMasksTheOnesAroundIt(t *testing.T) {
	env := newEnv(t)
	path := hostPath("s1")
	bashEvent(t, env, path, "s1", "echo "+tokGitLab)
	broken := `{"v":1,"payload":{"tool_input":{"command":"echo ` + tokGitLab + `"` // cut off
	foreign := `{"v":2,"ts":"2031-03-04T10:00:00Z","agent":"claude","profile":"claude-personal","runtime":"host","session_id":"s1","event":"PostToolUse","cwd":"/x","payload":{"command":"echo ` + tokGitLab + `"}}`
	env.WriteFile(path, env.ReadFile(path)+broken+"\n"+foreign+"\n")
	chmod600(t, env, path)
	bashEvent(t, env, path, "s1", "echo ghp_"+tokGitHub)

	res := env.Run("scrub")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	lines := strings.Split(strings.TrimSuffix(env.ReadFile(path), "\n"), "\n")
	if len(lines) != 4 || lines[1] != broken || lines[2] != foreign {
		t.Errorf("lines = %q, want the broken and the foreign line as they were", lines)
	}
	if !strings.Contains(lines[0], `"echo `+mask+`"`) || !strings.Contains(lines[0], `"masked":true`) {
		t.Errorf("first line = %q, want it masked", lines[0])
	}
	if !strings.Contains(lines[3], `"echo `+mask+`"`) || !strings.Contains(lines[3], `"masked":true`) {
		t.Errorf("last line = %q, want it masked", lines[3])
	}
	if want := env.Path(path) + ": 2 lines\nscrubbed 1 file, 2 lines\n"; res.Stdout != want {
		t.Errorf("stdout = %q, want %q", res.Stdout, want)
	}
}

func TestScrub_KeepsTheFieldsOfALineThatItDoesNotKnow(t *testing.T) {
	env := newEnv(t)
	path := hostPath("s1")
	line := `{"v":1,"ts":"2031-03-04T10:00:00Z","agent":"claude","profile":"claude-personal","runtime":"host","session_id":"s1","event":"PostToolUse","cwd":"/x","later":{"a":1},"payload":{"tool_input":{"command":"echo ` + tokGitLab + `"}}}`
	env.WriteFile(path, line+"\n")
	chmod600(t, env, path)

	if res := env.Run("scrub"); res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	want := `{"v":1,"ts":"2031-03-04T10:00:00Z","agent":"claude","profile":"claude-personal","runtime":"host","session_id":"s1","event":"PostToolUse","cwd":"/x","later":{"a":1},"payload":{"tool_input":{"command":"echo ` + mask + `"}},"masked":true}` + "\n"
	if got := env.ReadFile(path); got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

func TestScrub_DoesNotTouchAFileThatNeedsNoChange(t *testing.T) {
	env := newEnv(t)
	clean, dirty := hostPath("clean"), hostPath("dirty")
	bashEvent(t, env, clean, "clean", "go test ./...")
	bashEvent(t, env, dirty, "dirty", "echo "+tokGitLab)
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(env.Path(clean), old, old); err != nil {
		t.Fatal(err)
	}
	before := env.ReadFile(clean)

	res := env.Run("scrub")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if env.ReadFile(clean) != before || !env.ModTime(clean).Equal(old) {
		t.Errorf("the file that needed no change was touched (mtime %v, want %v)", env.ModTime(clean), old)
	}
	if strings.Contains(res.Stdout, "clean") {
		t.Errorf("stdout = %q, want the clean file not named", res.Stdout)
	}
}

func TestScrub_RunningItTwiceChangesNothingTheSecondTime(t *testing.T) {
	env := newEnv(t)
	path := hostPath("s1")
	bashEvent(t, env, path, "s1", "echo "+tokGitLab+" password=hunter2")
	if res := env.Run("scrub"); res.ExitCode != 0 {
		t.Fatalf("first run: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	bytesAfter, mtime := env.ReadFile(path), env.ModTime(path)

	res := env.Run("scrub")

	if res.ExitCode != 0 || res.Stdout != "nothing to scrub\n" || res.Stderr != "" {
		t.Errorf("exit code = %d, stdout=%q, stderr=%q, want nothing to scrub", res.ExitCode, res.Stdout, res.Stderr)
	}
	if env.ReadFile(path) != bytesAfter || !env.ModTime(path).Equal(mtime) {
		t.Error("the second run rewrote the file")
	}
}

func TestScrub_SaysSoWhenThereIsNothingAtAll(t *testing.T) {
	env := newEnv(t)

	res := env.Run("scrub")

	if res.ExitCode != 0 || res.Stdout != "nothing to scrub\n" || res.Stderr != "" {
		t.Errorf("exit code = %d, stdout=%q, stderr=%q", res.ExitCode, res.Stdout, res.Stderr)
	}
}

func TestScrub_DryRunListsWhatWouldChangeAndWritesNothing(t *testing.T) {
	env := newEnv(t)
	path := hostPath("s1")
	bashEvent(t, env, path, "s1", "echo "+tokGitLab)
	handoff := ".baton/projects/app/handoffs/2031-03-04-1/1.md"
	env.WriteFile(handoff, "run `echo "+tokGitLab+"`\n")
	chmod600(t, env, handoff)
	eventBefore, handoffBefore, mtime := env.ReadFile(path), env.ReadFile(handoff), env.ModTime(path)

	res := env.Run("scrub", "--dry-run")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := env.Path(path) + ": 1 line\n" + env.Path(handoff) + ": 1 line\nwould scrub 2 files, 2 lines\n"
	if res.Stdout != want {
		t.Errorf("stdout = %q, want %q", res.Stdout, want)
	}
	if env.ReadFile(path) != eventBefore || env.ReadFile(handoff) != handoffBefore || !env.ModTime(path).Equal(mtime) {
		t.Error("--dry-run wrote something")
	}
}

func TestScrub_UsageErrors(t *testing.T) {
	env := newEnv(t)
	for _, args := range [][]string{{"scrub", "now"}, {"scrub", "--older-than", "3d"}} {
		res := env.Run(args...)
		if res.ExitCode != 2 || !strings.Contains(res.Stderr, "usage: baton scrub [--dry-run]") {
			t.Errorf("%v: exit code = %d, stderr=%q, want a usage error", args, res.ExitCode, res.Stderr)
		}
	}
}

func TestScrub_MasksTheTextOfEveryHandoffAndDraftOfEveryProjectAndKeepsTheirMode(t *testing.T) {
	env := newEnv(t)
	writeSecrets(t, env, 0o600, "LITERAL=literalsecret1\n")
	dir := ".baton/projects/app/handoffs/2031-03-04-1/"
	other := ".baton/projects/web/handoffs/2031-03-05-1/2.md"
	env.WriteFile(dir+"1.md", "## Commands\n\n```\ncurl -H 'Authorization: Bearer abc.DEF-123_456~xyz' literalsecret1\n```\n")
	env.WriteFile(dir+"draft.md", "The key is\n-----BEGIN RSA PRIVATE KEY-----\nMIIEfake\n-----END RSA PRIVATE KEY-----\nand password: hunter2\n")
	env.WriteFile(dir+"2.md", "nothing to hide\n")
	env.WriteFile(other, "token="+tokGitLab+"\n")
	for _, p := range []string{dir + "1.md", dir + "draft.md", dir + "2.md", other} {
		chmod600(t, env, p)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(env.Path(dir+"2.md"), old, old); err != nil {
		t.Fatal(err)
	}

	res := env.Run("scrub")

	if res.ExitCode != 0 || res.Stderr != "" {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got, want := env.ReadFile(dir+"1.md"), "## Commands\n\n```\ncurl -H 'Authorization: Bearer "+mask+"' "+mask+"\n```\n"; got != want {
		t.Errorf("1.md = %q, want %q", got, want)
	}
	if got, want := env.ReadFile(dir+"draft.md"), "The key is\n"+mask+"\nand password: "+mask+"\n"; got != want {
		t.Errorf("draft.md = %q, want %q", got, want)
	}
	if got, want := env.ReadFile(other), "token="+mask+"\n"; got != want {
		t.Errorf("other project: %q, want %q", got, want)
	}
	if env.ReadFile(dir+"2.md") != "nothing to hide\n" || !env.ModTime(dir+"2.md").Equal(old) {
		t.Error("the Handoff that needed no change was touched")
	}
	for _, p := range []string{dir + "1.md", dir + "draft.md", other} {
		if mode := env.Mode(p); mode != 0o600 {
			t.Errorf("%s: mode = %v, want 0600", p, mode)
		}
	}
	if !strings.HasSuffix(res.Stdout, "scrubbed 3 files, 4 lines\n") {
		t.Errorf("stdout = %q, want the total of 3 files and 4 lines", res.Stdout)
	}
	if left, _ := filepath.Glob(env.Path(dir) + ".*"); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestScrub_AHandoffAfterwardsListsTheMaskedCommandAndLsStillWorks(t *testing.T) {
	env := newEnv(t)
	repo := handoffProjectRunning(t, env)
	bashEvent(t, env, hostPath("s1"), "s1", "curl -H 'Authorization: Bearer abc.DEF-123_456~xyz' "+tokGitLab)
	// bashEvent wrote its own cwd and times; put the Event where the Task is.
	env.WriteFile(hostPath("s1"), strings.ReplaceAll(env.ReadFile(hostPath("s1")), `"cwd":"/x"`, `"cwd":"`+repo+`"`))
	chmod600(t, env, hostPath("s1"))
	endFirstSession(t, env, repo)

	if res := env.Run("scrub"); res.ExitCode != 0 {
		t.Fatalf("scrub: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	if res := env.Run("ls"); res.ExitCode != 0 || !strings.Contains(res.Stdout, "ended") {
		t.Errorf("ls: exit code = %d, stdout=%q, want the ended Session", res.ExitCode, res.Stdout)
	}
	res := env.Run("handoff", handoffTask, "--to", "claude@claude-work")
	if res.ExitCode != 0 {
		t.Fatalf("handoff: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	got := env.ReadFile(handoffFile(env, "1"))
	if want := "\n```\ncurl -H 'Authorization: Bearer " + mask + "' " + mask + "\n```\n"; !strings.Contains(got, want) {
		t.Errorf("handoff lacks the masked command:\n%s", got)
	}
}

func TestScrub_ReportsAFileItCannotRewriteAndStillProcessesTheOthers(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	env := newEnv(t)
	locked, fine := appPath("locked"), hostPath("fine")
	bashEvent(t, env, locked, "locked", "echo "+tokGitLab)
	bashEvent(t, env, fine, "fine", "echo ghp_"+tokGitHub)
	dir := env.Path(filepath.Dir(locked))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	res := env.Run("scrub")

	if res.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "cannot scrub "+env.Path(locked)+": ") {
		t.Errorf("stderr = %q, want the file named", res.Stderr)
	}
	if strings.Contains(res.Stderr, tokGitLab) || strings.Contains(res.Stdout, tokGitLab) {
		t.Error("the output holds a secret")
	}
	if got := commandOf(t, env, fine, 0); got != "echo "+mask {
		t.Errorf("the other file: command = %q, want it masked", got)
	}
	if !strings.Contains(env.ReadFile(locked), tokGitLab) {
		t.Error("the file that could not be rewritten was changed")
	}
	if want := env.Path(fine) + ": 1 line\nscrubbed 1 file, 1 line\n"; res.Stdout != want {
		t.Errorf("stdout = %q, want %q: only what was rewritten counts", res.Stdout, want)
	}
}

// waitForLockWaiters waits until n processes are blocked on a lock of the file
// at path (relative to HOME).
func waitForLockWaiters(t *testing.T, env *batontest.Env, path string, n int) {
	t.Helper()
	info, err := os.Stat(env.Path(path))
	if err != nil {
		t.Fatal(err)
	}
	suffix := ":" + strconv.FormatUint(info.Sys().(*syscall.Stat_t).Ino, 10) + " "
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile("/proc/locks")
		if err != nil {
			t.Skipf("cannot tell who waits for a lock: %v", err)
		}
		waiting := 0
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "->") && strings.Contains(line, suffix) {
				waiting++
			}
		}
		if waiting >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("fewer than %d processes wait for the lock on %s", n, path)
}

func TestScrub_AHookThatWaitsForTheLockWhileScrubRenamesTheFileStillWritesItsLine(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	path := hostPath("s1")
	bashEvent(t, env, path, "s1", "echo "+tokGitLab)
	release := lockFile(t, env, path)

	scrub := env.Start("scrub")
	waitForLockWaiters(t, env, path, 1)
	done := make(chan batontest.Result, 1)
	go func() {
		done <- env.RunWithStdin(payload(t, "UserPromptSubmit", "s1", repo), hookArgs("claude-personal")...)
	}()
	waitForLockWaiters(t, env, path, 2)
	release()
	hook, res := <-done, scrub.Wait()

	if hook.ExitCode != 0 || res.ExitCode != 0 {
		t.Fatalf("hook: exit code = %d, stderr=%q; scrub: exit code = %d, stderr=%q", hook.ExitCode, hook.Stderr, res.ExitCode, res.Stderr)
	}
	lines := eventLines(t, env, path)
	if len(lines) != 2 || lines[1]["event"] != "UserPromptSubmit" {
		t.Fatalf("lines = %v, want the scrubbed line and the hook's", lines)
	}
	if got := commandOf(t, env, path, 0); got != "echo "+mask {
		t.Errorf("command = %q, want it masked", got)
	}
}
