package test

import (
	"os"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/loadkpi/baton/internal/batontest"
)

// stuckSession is a host Session whose Agent died without SessionEnd, in a
// repository, with a PID that is only the hook's parent (the Agent was not
// found among the ancestors): Baton cannot tell that it is gone. It returns
// the repository.
func stuckSession(t *testing.T, env *batontest.Env, sessionID string) string {
	t.Helper()
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	hostEvent(t, env, sessionID, "2031-03-04T10:00:00Z", "PreToolUse", repo,
		proc{host: thisHost(t), pid: deadPID(t), start: 4242, fallback: true})
	return repo
}

func TestSessionEnd_EndsASessionBatonCannotJudgeSoItNoLongerBlocksNew(t *testing.T) {
	env := newEnv(t)
	repo := stuckSession(t, env, "abcdef12-3456-7890-abcd-ef1234567890")

	blocked := env.Run("new", repo, "--agent", "claude")
	res := env.Run("session", "end", "abcdef12")
	unblocked := env.Run("new", repo, "--agent", "claude")

	if blocked.ExitCode != 1 || !strings.Contains(blocked.Stderr, "already has an active session") {
		t.Fatalf("the stuck Session did not block: exit code = %d, stderr=%q", blocked.ExitCode, blocked.Stderr)
	}
	if res.ExitCode != 0 {
		t.Fatalf("session end: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if unblocked.ExitCode != 0 {
		t.Errorf("an ended Session still blocks: exit code = %d, stderr=%q", unblocked.ExitCode, unblocked.Stderr)
	}
	rows := lsRows(t, env)
	if len(rows) != 1 || rows[0][6] != "ended" {
		t.Errorf("rows = %v, want one Session with status ended", rows)
	}
}

const stuckID = "abcdef12-3456-7890-abcd-ef1234567890"

func TestSessionEnd_AppendsOneEventOfBatonsOwnToTheSessionsFileAndLeavesTheAgentsLinesAlone(t *testing.T) {
	env := newEnv(t)
	repo := stuckSession(t, env, stuckID)
	path := ".baton/sessions/claude-" + stuckID + ".jsonl"
	before := env.ReadFile(path)

	res := env.Run("session", "end", stuckID) // the whole id works as well as its start

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.HasPrefix(env.ReadFile(path), before) {
		t.Errorf("the lines the Agent's hook wrote changed:\nbefore %q\nafter  %q", before, env.ReadFile(path))
	}
	lines := eventLines(t, env, path)
	if len(lines) != 2 {
		t.Fatalf("%d lines, want the hook's and one more", len(lines))
	}
	got := lines[1]
	want := map[string]any{
		"v": float64(1), "agent": "claude", "profile": "claude-personal", "runtime": "host",
		"session_id": stuckID, "event": "SessionEnd", "cwd": repo, "source": "baton",
		"payload": map[string]any{"reason": "ended by hand"},
	}
	for key, w := range want {
		if !reflect.DeepEqual(got[key], w) {
			t.Errorf("%s = %#v, want %#v", key, got[key], w)
		}
	}
	for _, key := range []string{"host", "agent_pid", "agent_start", "agent_pid_fallback", "container"} {
		if _, ok := got[key]; ok {
			t.Errorf("the line has %q, which says something about an Agent process Baton did not see", key)
		}
	}
	if _, ok := lines[0]["source"]; ok {
		t.Error("the hook's line got a source")
	}
	if ts, _ := got["ts"].(string); !strings.HasSuffix(ts, "Z") {
		t.Errorf("ts = %q, want a UTC time", ts)
	}
}

func TestSessionEnd_RefusesASessionWhoseAgentBatonSeesRunningNamingWhatItSawAndForceEndsItWithoutKillingAnything(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	agentProc, start := sleeper(t)
	pid := agentProc.Process.Pid
	hostEvent(t, env, stuckID, "2031-03-04T10:00:00Z", "PreToolUse", repo, proc{host: thisHost(t), pid: pid, start: start})
	path := ".baton/sessions/claude-" + stuckID + ".jsonl"
	before := env.ReadFile(path)

	refused := env.Run("session", "end", "abcdef12")

	if refused.ExitCode != 1 || !strings.Contains(refused.Stderr, "still running") ||
		!strings.Contains(refused.Stderr, "claude process "+strconv.Itoa(pid)+" is alive") ||
		!strings.Contains(refused.Stderr, "--force") {
		t.Errorf("exit code = %d, stderr=%q, want a refusal that names the live process and --force", refused.ExitCode, refused.Stderr)
	}
	if env.ReadFile(path) != before {
		t.Error("the refused command wrote to the Event file")
	}
	if rows := lsRows(t, env); len(rows) != 1 || rows[0][6] != "running" {
		t.Errorf("rows = %v, want the Session still running", rows)
	}

	forced := env.Run("session", "end", "--force", "abcdef12")

	if forced.ExitCode != 0 {
		t.Fatalf("--force: exit code = %d, stderr=%q", forced.ExitCode, forced.Stderr)
	}
	if rows := lsRows(t, env); len(rows) != 1 || rows[0][6] != "ended" {
		t.Errorf("rows = %v, want the Session ended", rows)
	}
	if state, _ := readStat(t, pid); state == "Z" || state == "X" {
		t.Errorf("the Agent's process is in state %s: it was killed", state)
	}
}

func TestSessionEnd_ReleasesAWaitingHandoff(t *testing.T) {
	env := newEnv(t)
	handoffProjectRunning(t, env) // its Session s1 reported no PID: nothing can judge it
	p := env.Start("handoff", handoffTask, "--to", "claude@claude-work")
	p.WaitStdout("waiting for the claude session s1 of " + handoffTask)
	if calls := env.Calls("guake"); len(calls) != 0 {
		t.Fatalf("guake was called while the Session was active: %v", calls)
	}

	if res := env.Run("session", "end", "s1"); res.ExitCode != 0 {
		t.Fatalf("session end: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	res := p.Wait()

	if res.ExitCode != 0 {
		t.Fatalf("handoff: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(env.ReadFile(handoffFile(env, "1")), "session s1 (ended)") {
		t.Errorf("handoff = %q, want the Session shown as ended", env.ReadFile(handoffFile(env, "1")))
	}
	if len(env.Calls("guake")) != 1 {
		t.Errorf("guake calls = %v, want one tab after the Session was ended", env.Calls("guake"))
	}
}

func TestSessionEnd_AnAmbiguousPrefixIsRefusedWithTheCandidatesAndAnUnknownOneToo(t *testing.T) {
	env := newEnv(t)
	repo := stuckSession(t, env, "abcdef12-0000")
	hostEvent(t, env, "abcdef12-1111", "2031-03-04T11:00:00Z", "Stop", repo, proc{})
	hostEvent(t, env, "ffff0000-2222", "2031-03-04T12:00:00Z", "Stop", repo, proc{})
	before := env.ReadFile(".baton/sessions/claude-abcdef12-0000.jsonl")

	ambiguous := env.Run("session", "end", "abcdef12")
	unknown := env.Run("session", "end", "0123")

	if ambiguous.ExitCode != 1 || !strings.Contains(ambiguous.Stderr, "2 sessions have an id starting with") {
		t.Errorf("exit code = %d, stderr=%q, want a refusal that says the id is ambiguous", ambiguous.ExitCode, ambiguous.Stderr)
	}
	// The candidates are listed like `baton ls` lists them, with the id as long as
	// the SESSION column makes it; the other Session is not.
	if !strings.Contains(ambiguous.Stderr, "abcdef12-0  ") || !strings.Contains(ambiguous.Stderr, "abcdef12-1  ") ||
		strings.Contains(ambiguous.Stderr, "ffff0000") || !strings.Contains(ambiguous.Stderr, "SESSION") {
		t.Errorf("stderr = %q, want the two candidates, as a table, and not the third Session", ambiguous.Stderr)
	}
	if unknown.ExitCode != 1 || !strings.Contains(unknown.Stderr, `no session has an id starting with "0123"`) {
		t.Errorf("exit code = %d, stderr=%q, want a refusal that says no Session matches", unknown.ExitCode, unknown.Stderr)
	}
	if env.ReadFile(".baton/sessions/claude-abcdef12-0000.jsonl") != before {
		t.Error("a refused command wrote to an Event file")
	}
	for _, row := range lsRows(t, env) {
		if row[6] == "ended" {
			t.Errorf("rows have an ended Session: %v", row)
		}
	}
}

func TestSessionEnd_ASessionThatHasEndedIsNotEndedAgain(t *testing.T) {
	env := newEnv(t)
	stuckSession(t, env, stuckID)
	if res := env.Run("session", "end", "abcdef12"); res.ExitCode != 0 {
		t.Fatalf("first end: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	path := ".baton/sessions/claude-" + stuckID + ".jsonl"
	before := env.ReadFile(path)

	res := env.Run("session", "end", "abcdef12")

	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "has already ended") {
		t.Errorf("exit code = %d, stdout=%q, stderr=%q, want a note that it has ended", res.ExitCode, res.Stdout, res.Stderr)
	}
	if env.ReadFile(path) != before {
		t.Error("a second Event was written")
	}
}

func TestSessionEnd_UsageErrorsExitWithTwo(t *testing.T) {
	env := newEnv(t)
	for name, args := range map[string][]string{
		"no subcommand":      {"session"},
		"unknown subcommand": {"session", "stop", "abc"},
		"no session":         {"session", "end"},
		"two sessions":       {"session", "end", "a", "b"},
		"unknown flag":       {"session", "end", "a", "--kill"},
	} {
		if res := env.Run(args...); res.ExitCode != 2 || !strings.HasPrefix(res.Stderr, "baton: session") {
			t.Errorf("%s: exit code = %d, stderr=%q, want 2 and a message about session", name, res.ExitCode, res.Stderr)
		}
	}
}

func TestSessionEnd_TheRefusalOfNewMentionsItOnlyWhenBatonCannotJudgeTheBlockingSession(t *testing.T) {
	hint := "baton session end " + stuckID
	t.Run("a PID that is only the hook's parent", func(t *testing.T) {
		env := newEnv(t)
		repo := stuckSession(t, env, stuckID)

		res := env.Run("new", repo, "--agent", "claude")

		if res.ExitCode != 1 || !strings.Contains(res.Stderr, hint) {
			t.Errorf("exit code = %d, stderr=%q, want it to mention %q", res.ExitCode, res.Stderr, hint)
		}
	})
	t.Run("no PID", func(t *testing.T) {
		env := newEnv(t)
		env.WriteConfig(newConfig(env, ""))
		repo := newRepo(t, env, "my-app")
		hostEvent(t, env, stuckID, "2031-03-04T10:00:00Z", "PreToolUse", repo, proc{})

		res := env.Run("new", repo, "--agent", "claude")

		if res.ExitCode != 1 || !strings.Contains(res.Stderr, hint) {
			t.Errorf("exit code = %d, stderr=%q, want it to mention %q", res.ExitCode, res.Stderr, hint)
		}
	})
	t.Run("a Runtime that did not answer", func(t *testing.T) {
		env := newEnv(t)
		launchProject(t, env)
		newOK(t, env)
		newFakeDocker(t, env, containerID("aaaaaaaaaaaa"))
		env.WriteFile("docker-fixtures/fail-ps", "")
		sandboxEvent(t, env, stuckID, "2031-03-04T10:00:00Z", "PreToolUse", proc{host: "aaaaaaaaaaaa", pid: 100, start: 5000})

		res := env.Run("new", "app", "--agent", "claude")

		if res.ExitCode != 1 || !strings.Contains(res.Stderr, hint) {
			t.Errorf("exit code = %d, stderr=%q, want it to mention %q", res.ExitCode, res.Stderr, hint)
		}
	})
	t.Run("a process Baton sees running", func(t *testing.T) {
		env := newEnv(t)
		env.WriteConfig(newConfig(env, ""))
		repo := newRepo(t, env, "my-app")
		pid, start := livePID(t)
		hostEvent(t, env, stuckID, "2031-03-04T10:00:00Z", "PreToolUse", repo, proc{host: thisHost(t), pid: pid, start: start})

		res := env.Run("new", repo, "--agent", "claude")

		if res.ExitCode != 1 || !strings.Contains(res.Stderr, "already has an active session") || strings.Contains(res.Stderr, "session end") {
			t.Errorf("exit code = %d, stderr=%q, want the refusal without the hint: Baton can see the Agent runs", res.ExitCode, res.Stderr)
		}
	})
}

func TestSessionEnd_TheWaitingLineOfHandoffMentionsItOnlyWhenBatonCannotJudgeTheSession(t *testing.T) {
	t.Run("cannot judge", func(t *testing.T) {
		env := newEnv(t)
		repo := handoffProject(t, env)
		hostEvent(t, env, stuckID, "2031-03-04T11:00:00Z", "SessionStart", repo,
			proc{host: thisHost(t), pid: deadPID(t), start: 4242, fallback: true})

		p := env.Start("handoff", handoffTask, "--to", "claude@claude-work")
		p.WaitStdout("baton session end " + stuckID)
		p.Interrupt()
		p.Wait()
	})
	t.Run("running", func(t *testing.T) {
		env := newEnv(t)
		repo := handoffProject(t, env)
		pid, start := livePID(t)
		hostEvent(t, env, stuckID, "2031-03-04T11:00:00Z", "SessionStart", repo, proc{host: thisHost(t), pid: pid, start: start})

		p := env.Start("handoff", handoffTask, "--to", "claude@claude-work")
		p.WaitStdout("waiting for the claude session " + stuckID)
		p.Interrupt()
		res := p.Wait()

		if strings.Contains(res.Stdout, "session end") {
			t.Errorf("stdout = %q, want no hint: Baton can see the Agent runs", res.Stdout)
		}
	})
}

func TestSessionEnd_EndsASandboxSessionInTheEventFileOfItsProjectAndStillRefusesOneRunningInItsContainer(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	newOK(t, env)
	id := containerID("aaaaaaaaaaaa")
	docker := newFakeDocker(t, env, id)
	docker.process(id, 100, "claude", 5000)
	path := ".baton/projects/app/sessions/claude-s1.jsonl"
	sandboxEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: "aaaaaaaaaaaa", pid: 100, start: 5000})

	refused := env.Run("session", "end", "s1")

	if refused.ExitCode != 1 || !strings.Contains(refused.Stderr, "claude process 100 is alive") {
		t.Errorf("exit code = %d, stderr=%q, want a refusal that names the process in the container", refused.ExitCode, refused.Stderr)
	}

	// The Agent died, but the PID the Event names is only the hook's parent.
	sandboxEvent(t, env, "s1", "2031-03-04T10:01:00Z", "PreToolUse", proc{host: "aaaaaaaaaaaa", pid: 100, start: 5000, fallback: true})
	blocked := env.Run("new", "app", "--agent", "claude")
	ended := env.Run("session", "end", "s1")
	unblocked := env.Run("new", "app", "--agent", "claude")

	if blocked.ExitCode != 1 || !strings.Contains(blocked.Stderr, "baton session end s1") {
		t.Errorf("exit code = %d, stderr=%q, want the Session to block and the hint", blocked.ExitCode, blocked.Stderr)
	}
	if ended.ExitCode != 0 {
		t.Fatalf("session end: exit code = %d, stderr=%q", ended.ExitCode, ended.Stderr)
	}
	lines := eventLines(t, env, path)
	if last := lines[len(lines)-1]; last["source"] != "baton" || last["event"] != "SessionEnd" || last["runtime"] != "sandbox" || last["profile"] != "app.claude" {
		t.Errorf("last line = %v, want Baton's SessionEnd in the Project's Event file", last)
	}
	if env.Exists(".baton/sessions/claude-s1.jsonl") {
		t.Error("an Event file was made for the Session on the host")
	}
	if unblocked.ExitCode != 0 {
		t.Errorf("an ended Session still blocks: exit code = %d, stderr=%q", unblocked.ExitCode, unblocked.Stderr)
	}
}

func TestSessionEnd_EndsAComposeSessionWhoseAgentBatonCannotFind(t *testing.T) {
	env := newEnv(t)
	_, c := composeLostProject(t, env)
	c.Process(100, "claude", 5000)
	composeEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: aaaa, pid: 100, start: 5000, fallback: true})

	blocked := env.Run("new", "app", "--agent", "claude")
	ended := env.Run("session", "end", "s1")
	unblocked := env.Run("new", "app", "--agent", "claude")

	if blocked.ExitCode != 1 || !strings.Contains(blocked.Stderr, "baton session end s1") {
		t.Errorf("exit code = %d, stderr=%q, want the Session to block and the hint", blocked.ExitCode, blocked.Stderr)
	}
	if ended.ExitCode != 0 {
		t.Fatalf("session end: exit code = %d, stderr=%q", ended.ExitCode, ended.Stderr)
	}
	events := eventsOf(t, env, "app", "s1")
	if last := events[len(events)-1]; last["source"] != "baton" || last["event"] != "SessionEnd" || last["runtime"] != "compose" {
		t.Errorf("last line = %v, want Baton's SessionEnd in the Project's Event file", last)
	}
	if unblocked.ExitCode != 0 {
		t.Errorf("an ended Session still blocks: exit code = %d, stderr=%q", unblocked.ExitCode, unblocked.Stderr)
	}
}

func TestSessionEnd_TheEventOfBatonIsNotACommandOfTheAgentInTheHandoff(t *testing.T) {
	env := newEnv(t)
	repo := handoffProjectRunning(t, env)
	toolEvent(t, env, repo, "2031-03-04T10:01:00Z", "PostToolUse", "go build ./...",
		map[string]any{"tool_response": map[string]any{"stdout": ""}})
	if res := env.Run("session", "end", "s1"); res.ExitCode != 0 {
		t.Fatalf("session end: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	res := env.Run("handoff", handoffTask, "--to", "claude@claude-work")

	if res.ExitCode != 0 {
		t.Fatalf("handoff: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := "1. exit 0, 2031-03-04T10:01:00Z\n\n```\ngo build ./...\n```\n\n\n## Narrative"
	if got := env.ReadFile(handoffFile(env, "1")); !strings.Contains(got, want) {
		t.Errorf("handoff = %s\nwant it to contain %q", got, want)
	}
}

func TestSessionEnd_TakesTheLockTheHookTakesOnTheEventFile(t *testing.T) {
	env := newEnv(t)
	stuckSession(t, env, stuckID)
	path := env.Path(".baton", "sessions", "claude-"+stuckID+".jsonl")
	before := env.ReadFile(".baton/sessions/claude-" + stuckID + ".jsonl")

	// A hook that is in the middle of its append holds this lock.
	held, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}

	p := env.Start("session", "end", "abcdef12")
	time.Sleep(300 * time.Millisecond) // nothing to wait for: the command is blocked and prints nothing
	if got := env.ReadFile(".baton/sessions/claude-" + stuckID + ".jsonl"); got != before {
		t.Fatalf("the Event file changed while the lock was held: %q", got)
	}

	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	res := p.Wait()

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if lines := eventLines(t, env, ".baton/sessions/claude-"+stuckID+".jsonl"); len(lines) != 2 || lines[1]["source"] != "baton" {
		t.Errorf("lines = %v, want the hook's and Baton's", lines)
	}
}
