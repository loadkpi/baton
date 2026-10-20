package test

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/loadkpi/baton/internal/batontest"
)

// watchInterval is the polling interval the watch tests pass as the real
// --interval flag: short enough to keep the tests fast.
const watchInterval = "50ms"

// startWatch starts `baton watch` and returns once it has read the Sessions
// that exist, which is when Events written afterwards count as new.
func startWatch(t *testing.T, env *batontest.Env) *batontest.Process {
	t.Helper()
	p := env.Start("watch", "--interval", watchInterval)
	p.WaitStdout("watching")
	return p
}

// notifications waits until the notify-send stub has been called n times and
// returns its calls. It fails the test when that does not happen in time.
func notifications(t *testing.T, env *batontest.Env, n int) []batontest.Call {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		calls := env.Calls("notify-send")
		if len(calls) >= n {
			return calls
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d notifications, have %v", n, calls)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// notifyArgs is the arguments of the calls to notify-send.
func notifyArgs(calls []batontest.Call) [][]string {
	out := make([][]string, len(calls))
	for i, c := range calls {
		out[i] = c.Args
	}
	return out
}

// watchRepo makes a Project my-app whose current Task is fix-login, the way
// `baton new` does, and returns its path and the process of a running Agent
// that Sessions can name.
func watchRepo(t *testing.T, env *batontest.Env) (repo string, p proc) {
	t.Helper()
	env.WriteConfig(newConfig(env, ""))
	repo = newRepo(t, env, "my-app")
	if res := env.Run("new", repo, "fix-login", "--agent", "claude"); res.ExitCode != 0 {
		t.Fatalf("new: %s", res.Stderr)
	}
	pid, start := livePID(t)
	return repo, proc{host: thisHost(t), pid: pid, start: start}
}

func TestWatch_AnApprovalRequestAfterStartNotifiesWithTheTaskAndTheAgent(t *testing.T) {
	env := newEnv(t)
	repo, p := watchRepo(t, env)
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
	startWatch(t, env)

	hostEvent(t, env, "s1", "2031-03-04T10:00:05Z", "PermissionRequest", repo, p)

	got := notifyArgs(notifications(t, env, 1))
	want := [][]string{{"-a", "baton", "Baton: waiting for approval", "my-app/fix-login (claude)"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("notify-send calls = %q, want %q", got, want)
	}
}

// othersAfterSentinel proves that the watcher has looked at everything written
// so far, and returns the notifications that are not the proof. It makes a
// second Project whose Session asks for approval with a time older than any
// other, so that it is told last: when its notification is there, so is every
// other one the same look had to send, and nothing needs a sleep.
func othersAfterSentinel(t *testing.T, env *batontest.Env) [][]string {
	t.Helper()
	repo := newRepo(t, env, "sentinel-app")
	if res := env.Run("new", repo, "sentinel", "--agent", "claude"); res.ExitCode != 0 {
		t.Fatalf("new: %s", res.Stderr)
	}
	pid, start := livePID(t)
	hostEvent(t, env, "sentinel", "2030-01-01T00:00:00Z", "PermissionRequest", repo, proc{host: thisHost(t), pid: pid, start: start})

	var others [][]string
	deadline := time.Now().Add(15 * time.Second)
	for {
		others = others[:0]
		found := false
		for _, args := range notifyArgs(env.Calls("notify-send")) {
			if args[len(args)-1] == "sentinel-app/sentinel (claude)" {
				found = true
			} else {
				others = append(others, args)
			}
		}
		if found {
			return others
		}
		if time.Now().After(deadline) {
			t.Fatalf("the sentinel was never notified; calls = %v", env.Calls("notify-send"))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWatch_WhatIsAlreadyTrueWhenItStartsIsNotNotified(t *testing.T) {
	env := newEnv(t)
	repo, p := watchRepo(t, env)
	hostEvent(t, env, "waiting", "2031-03-04T10:00:00Z", "PermissionRequest", repo, p)
	hostEvent(t, env, "gone", "2031-03-04T10:00:00Z", "PreToolUse", repo, proc{host: thisHost(t), pid: deadPID(t), start: 4242})
	startWatch(t, env)

	if others := othersAfterSentinel(t, env); len(others) != 0 {
		t.Errorf("notify-send calls = %q, want none for what was true at the start", others)
	}
}

func TestWatch_ASecondRequestOfTheSameSessionIsNotifiedAgain(t *testing.T) {
	env := newEnv(t)
	repo, p := watchRepo(t, env)
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PermissionRequest", repo, p)
	startWatch(t, env)

	hostEvent(t, env, "s1", "2031-03-04T10:00:10Z", "PostToolUse", repo, p)
	hostEvent(t, env, "s1", "2031-03-04T10:00:20Z", "PermissionRequest", repo, p)

	want := [][]string{{"-a", "baton", "Baton: waiting for approval", "my-app/fix-login (claude)"}}
	if got := othersAfterSentinel(t, env); !reflect.DeepEqual(got, want) {
		t.Errorf("notify-send calls = %q, want %q", got, want)
	}
}

// turn writes the Events of one turn of a Session that start at start and end
// with Stop after d, with a tool call in between.
func turn(t *testing.T, env *batontest.Env, id, repo string, p proc, start time.Time, d time.Duration) {
	t.Helper()
	at := func(offset time.Duration) string { return start.Add(offset).Format(time.RFC3339) }
	hostEvent(t, env, id, at(0), "UserPromptSubmit", repo, p)
	hostEvent(t, env, id, at(d/2), "PreToolUse", repo, p)
	hostEvent(t, env, id, at(d), "Stop", repo, p)
}

func TestWatch_ATurnThatTookMoreThanThirtySecondsNotifiesWhenItEnds(t *testing.T) {
	env := newEnv(t)
	repo, p := watchRepo(t, env)
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
	startWatch(t, env)

	turn(t, env, "s1", repo, p, time.Date(2031, 3, 4, 10, 1, 0, 0, time.UTC), 31*time.Second)

	want := [][]string{{"-a", "baton", "Baton: waiting for input", "my-app/fix-login (claude)"}}
	if got := othersAfterSentinel(t, env); !reflect.DeepEqual(got, want) {
		t.Errorf("notify-send calls = %q, want %q", got, want)
	}
}

func TestWatch_ATurnOfThirtySecondsOrLessNotifiesNothing(t *testing.T) {
	env := newEnv(t)
	repo, p := watchRepo(t, env)
	hostEvent(t, env, "exact", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
	hostEvent(t, env, "short", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
	startWatch(t, env)

	turn(t, env, "exact", repo, p, time.Date(2031, 3, 4, 10, 1, 0, 0, time.UTC), 30*time.Second)
	turn(t, env, "short", repo, p, time.Date(2031, 3, 4, 10, 1, 0, 0, time.UTC), 3*time.Second)

	if got := othersAfterSentinel(t, env); len(got) != 0 {
		t.Errorf("notify-send calls = %q, want none", got)
	}
}

func TestWatch_AStopWithNoPromptBeforeItIsNotATurn(t *testing.T) {
	// The turn is judged by the Events' times, not by how long the Session has
	// been waiting or how long the watcher has been looking, so a Stop two
	// hours after the start of the Session says nothing about a turn.
	env := newEnv(t)
	repo, p := watchRepo(t, env)
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
	hostEvent(t, env, "stop-only", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
	startWatch(t, env)

	turn(t, env, "s1", repo, p, time.Date(2031, 3, 4, 10, 1, 0, 0, time.UTC), 2*time.Second)
	hostEvent(t, env, "stop-only", "2031-03-04T12:00:00Z", "Stop", repo, p) // no prompt before it

	if got := othersAfterSentinel(t, env); len(got) != 0 {
		t.Errorf("notify-send calls = %q, want none", got)
	}
}

func TestWatch_AHostAgentThatDiesAfterTheStartNotifiesThatTheSessionIsLost(t *testing.T) {
	env := newEnv(t)
	repo, _ := watchRepo(t, env)
	agentProc, start := sleeper(t)
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", repo, proc{host: thisHost(t), pid: agentProc.Process.Pid, start: start})
	startWatch(t, env)

	_ = agentProc.Process.Kill()
	_ = agentProc.Wait() // a zombie still counts as running

	want := [][]string{{"-a", "baton", "Baton: session lost", "my-app/fix-login (claude)"}}
	if got := othersAfterSentinel(t, env); !reflect.DeepEqual(got, want) {
		t.Errorf("notify-send calls = %q, want %q", got, want)
	}
}

func TestWatch_ALostSessionIsNotifiedOnce(t *testing.T) {
	env := newEnv(t)
	repo, _ := watchRepo(t, env)
	agentProc, start := sleeper(t)
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", repo, proc{host: thisHost(t), pid: agentProc.Process.Pid, start: start})
	startWatch(t, env)

	_ = agentProc.Process.Kill()
	_ = agentProc.Wait()
	notifications(t, env, 1)

	if got := othersAfterSentinel(t, env); len(got) != 1 {
		t.Errorf("notify-send calls = %q, want one", got)
	}
}

// sandboxWatch makes the sandbox Project "app" with its current Task fix-login,
// a container that runs, and a Claude in it.
func sandboxWatch(t *testing.T, env *batontest.Env) (*fakeDocker, string) {
	t.Helper()
	launchProject(t, env)
	if res := env.Run("new", "app", "fix-login", "--agent", "claude"); res.ExitCode != 0 {
		t.Fatalf("new: %s", res.Stderr)
	}
	id := containerID("aaaaaaaaaaaa")
	docker := newFakeDocker(t, env, id)
	docker.process(id, 100, "claude", 5000)
	return docker, id
}

func TestWatch_ASandboxSessionIsNotifiedFromTheHostWhenAskingForApprovalAndWhenItsContainerIsGone(t *testing.T) {
	env := newEnv(t)
	docker, _ := sandboxWatch(t, env)
	agentProc := proc{host: "aaaaaaaaaaaa", pid: 100, start: 5000}
	sandboxEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", agentProc)
	startWatch(t, env)

	sandboxEvent(t, env, "s1", "2031-03-04T10:00:05Z", "PermissionRequest", agentProc)
	notifications(t, env, 1)
	docker.setRunning() // the container is gone

	notifications(t, env, 2)
	want := [][]string{
		{"-a", "baton", "Baton: waiting for approval", "app/fix-login (claude)"},
		{"-a", "baton", "Baton: session lost", "app/fix-login (claude)"},
	}
	if got := notifyArgs(env.Calls("notify-send")); !reflect.DeepEqual(got, want) {
		t.Errorf("notify-send calls = %q, want %q", got, want)
	}
}

func TestWatch_ALookAsksDockerPsOnceAndEachContainerOnceHoweverManySessionsTheyHold(t *testing.T) {
	env := newEnv(t)
	docker, _ := sandboxWatch(t, env)
	agentProc := proc{host: "aaaaaaaaaaaa", pid: 100, start: 5000}
	sandboxEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", agentProc)
	sandboxEvent(t, env, "s2", "2031-03-04T10:00:00Z", "PreToolUse", agentProc)
	startWatch(t, env)
	before := len(docker.calls("ps"))

	// Every look is one `docker ps`, however many Sessions run in containers.
	deadline := time.Now().Add(15 * time.Second)
	for len(docker.calls("ps")) < before+3 {
		if time.Now().After(deadline) {
			t.Fatal("watch did not look three times")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got, looks := len(docker.calls("exec")), len(docker.calls("ps")); got > looks {
		t.Errorf("docker exec calls = %d for %d docker ps calls, want at most one per container per look", got, looks)
	}
}

func TestWatch_ADockerThatStopsAnsweringDoesNotMakeALostSessionLostAgain(t *testing.T) {
	env := newEnv(t)
	docker, _ := sandboxWatch(t, env)
	sandboxEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: "aaaaaaaaaaaa", pid: 100, start: 5000})
	p := startWatch(t, env)
	docker.setRunning() // the container is gone
	notifications(t, env, 1)

	// While docker does not answer the Session shows the status of its Events
	// again; when it answers again it is lost, as before.
	env.WriteFile("docker-fixtures/fail-ps", "")
	p.WaitStderr("baton: warning: cannot tell whether sandbox sessions are still running")
	if err := os.Remove(env.Path("docker-fixtures", "fail-ps")); err != nil {
		t.Fatal(err)
	}

	if got := othersAfterSentinel(t, env); len(got) != 1 {
		t.Errorf("notify-send calls = %q, want only the first one", got)
	}
}

func TestWatch_ADockerThatDoesNotAnswerIsReportedOnceAndTheWatcherGoesOn(t *testing.T) {
	env := newEnv(t)
	docker, _ := sandboxWatch(t, env)
	agentProc := proc{host: "aaaaaaaaaaaa", pid: 100, start: 5000}
	sandboxEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", agentProc)
	env.WriteFile("docker-fixtures/fail-ps", "")
	p := startWatch(t, env)
	p.WaitStderr("Cannot connect to the Docker daemon")
	looks := len(docker.calls("ps"))
	for len(docker.calls("ps")) < looks+3 { // a few more looks, none of which repeats the warning
		time.Sleep(5 * time.Millisecond)
	}

	sandboxEvent(t, env, "s1", "2031-03-04T10:00:05Z", "PermissionRequest", agentProc)

	if got := len(othersAfterSentinel(t, env)); got != 1 {
		t.Errorf("notifications = %d, want the approval request to be told although docker is down", got)
	}
	if got := strings.Count(p.Stderr(), "baton: warning: cannot tell whether"); got != 1 {
		t.Errorf("stderr = %q, want the warning once", p.Stderr())
	}
}

func TestWatch_AHookNeverSendsANotification(t *testing.T) {
	env := newEnv(t)
	repo, _ := watchRepo(t, env)

	res := runHook(t, env, "claude-personal", "PermissionRequest", "s1", repo)

	if res.ExitCode != 0 {
		t.Fatalf("hook: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := env.Calls("notify-send"); len(got) != 0 {
		t.Errorf("notify-send calls = %v, want none: only baton watch notifies", got)
	}
}

func TestWatch_WithoutTheWatcherListingAndHooksWork(t *testing.T) {
	env := newEnv(t)
	repo, _ := watchRepo(t, env)
	p := startWatch(t, env)
	res := p.Stop()
	if res.ExitCode != 0 {
		t.Fatalf("watch: exit code after SIGTERM = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	runHook(t, env, "claude-personal", "SessionStart", "s1", repo)
	rows := lsRows(t, env)

	if len(rows) != 1 || rows[0][5] != "waiting_input" {
		t.Errorf("rows = %v, want the Session the hook recorded", rows)
	}
}

const wantUnit = `[Unit]
Description=Baton: desktop notifications when an Agent needs you

[Service]
ExecStart=%h/.baton/bin/baton watch
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`

// systemctlCalls is the arguments of the calls to the systemctl stub.
func systemctlCalls(env *batontest.Env) [][]string {
	return notifyArgs(env.Calls("systemctl"))
}

func TestWatchInstall_WritesTheUserUnitInsideHomeAndEnablesIt(t *testing.T) {
	env := newEnv(t)
	env.WriteFile(".baton/bin/baton", "#!/bin/sh\n", true)

	res := env.Run("watch", "install")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := env.ReadFile(".config/systemd/user/baton-watch.service"); got != wantUnit {
		t.Errorf("unit = %q, want %q", got, wantUnit)
	}
	want := [][]string{{"--user", "daemon-reload"}, {"--user", "enable", "--now", "baton-watch.service"}}
	if got := systemctlCalls(env); !reflect.DeepEqual(got, want) {
		t.Errorf("systemctl calls = %q, want %q", got, want)
	}
	if res.Stderr != "" {
		t.Errorf("stderr = %q, want nothing", res.Stderr)
	}
	if want := "wrote " + env.Path(".config/systemd/user/baton-watch.service") + "\nenabled and started baton-watch.service\n"; res.Stdout != want {
		t.Errorf("stdout = %q, want %q", res.Stdout, want)
	}
}

func TestWatchInstall_RunningItAgainRewritesTheSameUnitAndEnablesItAgain(t *testing.T) {
	env := newEnv(t)
	env.WriteFile(".baton/bin/baton", "#!/bin/sh\n", true)
	env.Run("watch", "install")

	res := env.Run("watch", "install")

	if res.ExitCode != 0 || env.ReadFile(".config/systemd/user/baton-watch.service") != wantUnit {
		t.Errorf("exit code = %d, stderr=%q, unit=%q", res.ExitCode, res.Stderr, env.ReadFile(".config/systemd/user/baton-watch.service"))
	}
	if got := len(systemctlCalls(env)); got != 4 {
		t.Errorf("systemctl calls = %d, want 4", got)
	}
}

func TestWatchInstall_WarnsWhenTheBinaryTheUnitStartsIsNotThere(t *testing.T) {
	env := newEnv(t)

	res := env.Run("watch", "install")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if want := "baton: warning: " + env.Path(".baton/bin/baton") + " does not exist yet; run make install, or the unit cannot start baton watch\n"; res.Stderr != want {
		t.Errorf("stderr = %q, want %q", res.Stderr, want)
	}
}

func TestWatchInstall_AFailingSystemctlIsReportedAndTheUnitStaysWritten(t *testing.T) {
	env := newEnv(t)
	env.WriteFile(".baton/bin/baton", "#!/bin/sh\n", true)
	env.Stub("systemctl", batontest.WithScript(`echo "Failed to connect to bus: No medium found" >&2; exit 1`))

	res := env.Run("watch", "install")

	if res.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", res.ExitCode)
	}
	if want := "baton: systemctl --user daemon-reload: exit status 1: Failed to connect to bus: No medium found\n"; res.Stderr != want {
		t.Errorf("stderr = %q, want %q", res.Stderr, want)
	}
	if got := env.ReadFile(".config/systemd/user/baton-watch.service"); got != wantUnit {
		t.Errorf("unit = %q, want it written before systemctl was asked", got)
	}
}

func TestWatch_BadArgumentsAreUsageErrors(t *testing.T) {
	env := newEnv(t)
	for _, args := range [][]string{
		{"watch", "uninstall"},
		{"watch", "install", "now"},
		{"watch", "--interval", "0"},
		{"watch", "--interval", "soon"},
		{"watch", "--now"},
	} {
		res := env.Run(args...)
		if res.ExitCode != 2 || !strings.HasPrefix(res.Stderr, "baton: watch: ") {
			t.Errorf("baton %v: exit code = %d, stderr=%q, want a usage error", args, res.ExitCode, res.Stderr)
		}
	}
	if got := systemctlCalls(env); len(got) != 0 {
		t.Errorf("systemctl calls = %q, want none", got)
	}
}

func TestWatch_StartsWithNoSessionsAndSaysWhatItIsDoing(t *testing.T) {
	env := newEnv(t)

	p := startWatch(t, env)

	if got, want := p.Stdout(), "watching 0 sessions every 50ms\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got := p.Stderr(); got != "" {
		t.Errorf("stderr = %q, want nothing", got)
	}
}
