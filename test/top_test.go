package test

import (
	"strings"
	"testing"

	"github.com/loadkpi/baton/internal/batontest"
)

const (
	enterScreen = "\x1b[?1049h"
	leaveScreen = "\x1b[?1049l"
)

// startTop starts `baton top` on a pseudo-terminal that redraws every 50 ms.
func startTop(env *batontest.Env) *batontest.Terminal {
	return env.StartTerminal("top", "--interval", "50ms")
}

// quitTop types q and checks that baton leaves the alternate screen and exits.
func quitTop(t *testing.T, term *batontest.Terminal) batontest.Result {
	t.Helper()
	term.Send("q")
	term.Expect(leaveScreen)
	res := term.Wait()
	if res.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0; transcript:\n%s", res.ExitCode, res.Stdout)
	}
	return res
}

// guakeMethods are the Guake calls the gdbus stub recorded, as "method args".
func guakeMethods(env *batontest.Env) []string {
	var out []string
	for _, c := range env.Calls("gdbus") {
		for i, a := range c.Args {
			if a == "--method" && i+1 < len(c.Args) {
				name := c.Args[i+1][strings.LastIndex(c.Args[i+1], ".")+1:]
				out = append(out, strings.Join(append([]string{name}, c.Args[i+2:]...), " "))
			}
		}
	}
	return out
}

// topSessions makes three Sessions in three repositories, the newest first:
// my-app waits for approval, old-app is lost, calm-app waits for input.
func topSessions(t *testing.T, env *batontest.Env) {
	t.Helper()
	env.WriteConfig(newConfig(env, ""))
	gone := proc{host: thisHost(t), pid: deadPID(t), start: 4242}
	hostEvent(t, env, "aaaa1111-0000-0000-0000-000000000001", "2031-03-04T10:00:00Z", "SessionStart", newRepo(t, env, "calm-app"),
		proc{host: thisHost(t), pid: deadPID(t), start: 4242, fallback: true})
	hostEvent(t, env, "bbbb2222-0000-0000-0000-000000000002", "2031-03-04T11:00:00Z", "PreToolUse", newRepo(t, env, "old-app"), gone)
	hostEvent(t, env, "cccc3333-0000-0000-0000-000000000003", "2031-03-04T12:00:00Z", "PermissionRequest", newRepo(t, env, "my-app"),
		proc{host: thisHost(t), pid: deadPID(t), start: 4242, fallback: true})
}

func TestTop_WithoutATerminalSaysSoAndExitsWithUsageError(t *testing.T) {
	env := newEnv(t)

	res := env.RunWithStdin("", "top")

	if res.ExitCode != 2 || !strings.Contains(res.Stderr, "must be a terminal") {
		t.Errorf("exit code = %d, stderr=%q, want 2 and a message about the terminal", res.ExitCode, res.Stderr)
	}
}

func TestTop_ShowsTheTableWithMarkersForApprovalAndLostAndCountsWhoWaits(t *testing.T) {
	env := newEnv(t)
	topSessions(t, env)

	term := startTop(env)
	term.Expect(enterScreen)
	term.Expect("baton top: 3 sessions, 2 waiting for you")
	term.Expect("PROJECT")
	term.Expect("STATUS")
	term.Expect("> ! my-app")
	term.Expect("waiting_approval")
	term.Expect("  x old-app")
	term.Expect("lost")
	term.Expect("    calm-app")
	term.Expect("waiting_input")
	term.Expect("Enter jump")
	quitTop(t, term)
}

func TestTop_QuitRestoresTheScreenAndCtrlCDoesToo(t *testing.T) {
	for _, key := range []string{"q", "\x03"} {
		env := newEnv(t)
		topSessions(t, env)
		term := startTop(env)
		term.Expect("3 sessions")

		term.Send(key)
		term.Expect("\x1b[?25h") // the cursor is shown again
		term.Expect(leaveScreen)
		res := term.Wait()

		if res.ExitCode != 0 {
			t.Errorf("key %q: exit code = %d, want 0", key, res.ExitCode)
		}
	}
}

func TestTop_ArrowsAndJKMoveTheSelection(t *testing.T) {
	env := newEnv(t)
	topSessions(t, env)
	term := startTop(env)
	term.Expect("> ! my-app")

	term.Send("\x1b[B")
	term.Expect("> x old-app")
	term.Send("j")
	term.Expect(">   calm-app")
	term.Send("\x1b[A")
	term.Expect("> x old-app")
	term.Send("k")
	term.Expect("> ! my-app")
	quitTop(t, term)
}

func TestTop_TheSelectionStaysOnTheSameSessionWhenAnotherAppears(t *testing.T) {
	env := newEnv(t)
	topSessions(t, env)
	term := startTop(env)
	term.Send("jj")
	term.Expect(">   calm-app")

	hostEvent(t, env, "dddd4444-0000-0000-0000-000000000004", "2031-03-04T13:00:00Z", "SessionStart", newRepo(t, env, "new-app"),
		proc{host: thisHost(t), pid: deadPID(t), start: 4242, fallback: true})
	term.Expect("baton top: 4 sessions")
	term.Expect(">   calm-app")
	quitTop(t, term)
}

func TestTop_EnterShowsTheGuakeTabWithTheTitleOfTheSession(t *testing.T) {
	env := newEnv(t)
	topSessions(t, env)
	env.Stub("gdbus", env.GdbusTabs("shell", "my-app/2031-03-04-1:claude", "other"))
	term := startTop(env)
	term.Expect("> ! my-app")

	term.Send("\r")
	term.Expect("showing its Guake tab")
	quitTop(t, term)

	got := guakeMethods(env)
	for _, want := range []string{"get_tab_count", "get_tab_name 1", "select_tab 1", "show"} {
		if !contains(got, want) {
			t.Errorf("gdbus calls = %q, want %q among them", got, want)
		}
	}
}

func TestTop_EnterFindsTheTabNamedAfterTheTasksName(t *testing.T) {
	env := newEnv(t)
	topSessions(t, env)
	env.Run("ls") // reading registers the Projects
	if res := env.Run("task", "name", "my-app/2031-03-04-1", "login-fix"); res.ExitCode != 0 {
		t.Fatalf("task name: %s", res.Stderr)
	}
	env.Stub("gdbus", env.GdbusTabs("my-app/login-fix:claude"))
	term := startTop(env)
	term.Expect("> ! my-app")

	term.Send("\r")
	term.Expect("showing its Guake tab")
	quitTop(t, term)

	if got := guakeMethods(env); !contains(got, "select_tab 0") {
		t.Errorf("gdbus calls = %q, want select_tab 0", got)
	}
}

func TestTop_EnterWithoutAMatchingTabSaysSoAndSelectsNothing(t *testing.T) {
	env := newEnv(t)
	topSessions(t, env)
	env.Stub("gdbus", env.GdbusTabs("shell", "some-other/tab:claude"))
	term := startTop(env)
	term.Expect("> ! my-app")

	term.Send("\r")
	term.Expect("no Guake tab for this session")
	quitTop(t, term)

	for _, m := range guakeMethods(env) {
		if strings.HasPrefix(m, "select_tab") || m == "show" {
			t.Errorf("gdbus call %q, want none that changes the tab", m)
		}
	}
}

func TestTop_EnterWithoutGdbusSaysSo(t *testing.T) {
	env := newEnv(t)
	topSessions(t, env)
	env.Without("gdbus")
	term := startTop(env)
	term.Expect("> ! my-app")

	term.Send("\r")
	term.Expect("cannot reach Guake")
	quitTop(t, term)
}

func TestTop_EndAsksAndYEndsASessionBatonCannotJudge(t *testing.T) {
	env := newEnv(t)
	stuckSession(t, env, stuckID)
	term := startTop(env)
	term.Expect("> ")

	term.Send("e")
	term.Expect("End session " + stuckID + "? [y/N]")
	term.Send("y")
	term.Expect("ended session " + stuckID)
	quitTop(t, term)

	if rows := lsRows(t, env); len(rows) != 1 || rows[0][6] != "ended" {
		t.Errorf("rows = %v, want the Session ended", rows)
	}
}

func TestTop_EndThenNLeavesTheSessionAlone(t *testing.T) {
	env := newEnv(t)
	stuckSession(t, env, stuckID)
	term := startTop(env)
	term.Expect("> ")

	term.Send("e")
	term.Expect("[y/N]")
	term.Send("n")
	term.Expect("not ended")
	quitTop(t, term)

	if rows := lsRows(t, env); len(rows) != 1 || rows[0][6] == "ended" {
		t.Errorf("rows = %v, want the Session not ended", rows)
	}
}

func TestTop_EndRefusesARunningSessionWithTheMessageOfSessionEnd(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	pid, start := livePID(t)
	hostEvent(t, env, stuckID, "2031-03-04T10:00:00Z", "PreToolUse", repo, proc{host: thisHost(t), pid: pid, start: start})
	term := startTop(env)
	term.Expect("> ")

	term.Send("e")
	term.Expect("[y/N]")
	term.Send("y")
	term.Expect("is still running")
	quitTop(t, term)

	if rows := lsRows(t, env); len(rows) != 1 || rows[0][6] != "running" {
		t.Errorf("rows = %v, want the Session still running", rows)
	}
}
