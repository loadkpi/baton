package test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/loadkpi/baton/internal/batontest"
)

// hostAgentEvent appends an Event of a host Session of the Agent.
func hostAgentEvent(t *testing.T, env *batontest.Env, agentName, sessionID, ts, event, cwd string, p proc) {
	t.Helper()
	writeAgentProcEvent(t, env, agentName, ".baton/sessions/"+agentName+"-"+sessionID+".jsonl", "host", agentName+"-personal", sessionID, ts, event, cwd, p, map[string]any{})
}

// sandboxAgentEvent appends an Event of a sandbox Session of Project app.
func sandboxAgentEvent(t *testing.T, env *batontest.Env, agentName, sessionID, ts, event string, p proc) {
	t.Helper()
	writeAgentProcEvent(t, env, agentName, ".baton/projects/app/sessions/"+agentName+"-"+sessionID+".jsonl", "sandbox", "app."+agentName, sessionID, ts, event, "/workspace", p, map[string]any{})
}

func TestCodexGone_HostCodexAfterStopOrInterruptHasEnded(t *testing.T) {
	for _, event := range []string{"Stop", "Interrupt"} {
		t.Run(event, func(t *testing.T) {
			env := newEnv(t)
			repo := newRepo(t, env, "my-app")
			hostAgentEvent(t, env, "codex", "s1", "2031-03-04T10:00:00Z", event, repo, proc{host: thisHost(t), pid: deadPID(t), start: 4242})

			rows := lsRows(t, env)

			if !reflect.DeepEqual(statuses(rows), []string{"ended"}) {
				t.Errorf("rows = %v, want one Session with status ended", rows)
			}
		})
	}
}

func TestCodexGone_HostCodexInAnyOtherStateIsLost(t *testing.T) {
	for _, event := range []string{"UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest", "SessionStart"} {
		t.Run(event, func(t *testing.T) {
			env := newEnv(t)
			repo := newRepo(t, env, "my-app")
			hostAgentEvent(t, env, "codex", "s1", "2031-03-04T10:00:00Z", event, repo, proc{host: thisHost(t), pid: deadPID(t), start: 4242})

			rows := lsRows(t, env)

			if !reflect.DeepEqual(statuses(rows), []string{"lost"}) {
				t.Errorf("rows = %v, want one Session with status lost", rows)
			}
		})
	}
}

func TestCodexGone_HostClaudeAfterStopIsStillLost(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "Stop", repo, proc{host: thisHost(t), pid: deadPID(t), start: 4242})

	rows := lsRows(t, env)

	if !reflect.DeepEqual(statuses(rows), []string{"lost"}) {
		t.Errorf("rows = %v, want one Session with status lost", rows)
	}
}

func TestCodexGone_ACodexThatIsStillRunningAfterStopWaitsForInput(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	pid, start := livePID(t)
	hostAgentEvent(t, env, "codex", "s1", "2031-03-04T10:00:00Z", "Stop", repo, proc{host: thisHost(t), pid: pid, start: start})

	rows := lsRows(t, env)

	if !reflect.DeepEqual(statuses(rows), []string{"waiting_input"}) {
		t.Errorf("rows = %v, want one Session with status waiting_input", rows)
	}
}

func TestCodexGone_ALateSessionEndChangesNothingVisibleAndNothingIsWritten(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	p := proc{host: thisHost(t), pid: deadPID(t), start: 4242}
	hostAgentEvent(t, env, "codex", "s1", "2031-03-04T10:00:00Z", "Stop", repo, p)
	before := env.ReadFile(".baton/sessions/codex-s1.jsonl")
	rowsBefore := lsRows(t, env)

	hostAgentEvent(t, env, "codex", "s1", "2031-03-04T10:00:05Z", "SessionEnd", repo, p)
	rowsAfter := lsRows(t, env)

	if !reflect.DeepEqual(statuses(rowsBefore), []string{"ended"}) || !reflect.DeepEqual(statuses(rowsAfter), []string{"ended"}) {
		t.Errorf("rows before = %v, after = %v, want ended both times", rowsBefore, rowsAfter)
	}
	if got := env.ReadFile(".baton/sessions/codex-s1.jsonl"); !strings.HasPrefix(got, before) || strings.Count(got, "\n") != 2 {
		t.Errorf("Event file = %q, want the Stop line unchanged and only the SessionEnd line added", got)
	}
}

func TestCodexGone_ACodexGoneAfterStopNoLongerBlocksNewInItsWorkingCopy(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	running, start := livePID(t)
	hostAgentEvent(t, env, "codex", "s1", "2031-03-04T10:00:00Z", "Stop", repo, proc{host: thisHost(t), pid: running, start: start})

	blocked := env.Run("new", repo, "--agent", "claude")
	hostAgentEvent(t, env, "codex", "s1", "2031-03-04T10:01:00Z", "Stop", repo, proc{host: thisHost(t), pid: running, start: start + 1})
	unblocked := env.Run("new", repo, "--agent", "claude")

	if blocked.ExitCode != 1 || !strings.Contains(blocked.Stderr, "already has an active session") {
		t.Errorf("a waiting Session did not block: exit code = %d, stderr=%q", blocked.ExitCode, blocked.Stderr)
	}
	if unblocked.ExitCode != 0 {
		t.Errorf("an ended Session still blocks: exit code = %d, stderr=%q", unblocked.ExitCode, unblocked.Stderr)
	}
}

func TestCodexGone_SandboxCodexWhoseContainerIsGoneAfterStopHasEndedAndAfterRunningIsLost(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	newOK(t, env)
	newFakeDocker(t, env, containerID("bbbbbbbbbbbb")) // another container runs
	p := proc{host: "aaaaaaaaaaaa", pid: 100, start: 5000}
	sandboxAgentEvent(t, env, "codex", "waiting", "2031-03-04T10:00:00Z", "Stop", p)
	sandboxAgentEvent(t, env, "codex", "busy", "2031-03-04T10:00:00Z", "PreToolUse", p)
	sandboxAgentEvent(t, env, "claude", "claudewt", "2031-03-04T10:00:00Z", "Stop", p)

	rows := lsRows(t, env)

	got := map[string]string{}
	for _, r := range rows {
		got[r[3]] = r[6]
	}
	want := map[string]string{"waiting": "ended", "busy": "lost", "claudewt": "lost"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("statuses by session = %v, want %v (rows %v)", got, want, rows)
	}
}

func TestCodexGone_WatchSendsNothingForACodexThatLeftWhileWaitingForInput(t *testing.T) {
	env := newEnv(t)
	repo, _ := watchRepo(t, env)
	agentProc, start := sleeper(t)
	p := proc{host: thisHost(t), pid: agentProc.Process.Pid, start: start}
	// A turn of 31 s that ends in Stop: the watcher would tell it, were the
	// Session not gone, and it saw the Session as waiting_input before.
	at := func(sec int) string { return time.Date(2031, 3, 4, 10, 0, sec, 0, time.UTC).Format(time.RFC3339) }
	hostAgentEvent(t, env, "codex", "s1", at(0), "UserPromptSubmit", repo, p)
	hostAgentEvent(t, env, "codex", "s1", at(31), "Stop", repo, p)
	startWatch(t, env)

	_ = agentProc.Process.Kill()
	_ = agentProc.Wait()

	if got := othersAfterSentinel(t, env); len(got) != 0 {
		t.Errorf("notify-send calls = %q, want none", got)
	}
	if rows := lsRows(t, env); !contains(statuses(rows), "ended") {
		t.Errorf("rows = %v, want the Codex Session ended", rows)
	}
}

func TestCodexGone_WatchTellsACodexThatDiedWhileRunningIsLost(t *testing.T) {
	env := newEnv(t)
	repo, _ := watchRepo(t, env)
	agentProc, start := sleeper(t)
	hostAgentEvent(t, env, "codex", "s1", "2031-03-04T10:00:00Z", "PreToolUse", repo, proc{host: thisHost(t), pid: agentProc.Process.Pid, start: start})
	startWatch(t, env)

	_ = agentProc.Process.Kill()
	_ = agentProc.Wait()

	want := [][]string{{"-a", "baton", "Baton: session lost", "my-app/fix-login (codex)"}}
	if got := othersAfterSentinel(t, env); !reflect.DeepEqual(got, want) {
		t.Errorf("notify-send calls = %q, want %q", got, want)
	}
}
