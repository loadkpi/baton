package test

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/loadkpi/baton/internal/batontest"
)

// handoffTask is the Task of the Sessions that handoffProject writes: it is
// named after the day of their first Event.
const handoffTask = "my-app/2031-03-04-1"

// handoffProject is a host Project "my-app" with one commit, two Claude
// Profiles, and one Claude Session of claude-personal in it that has ended. It
// returns the Project's path.
func handoffProject(t *testing.T, env *batontest.Env) string {
	t.Helper()
	repo := handoffProjectRunning(t, env)
	endFirstSession(t, env, repo)
	return repo
}

// endFirstSession ends the Session that handoffProjectRunning left running.
func endFirstSession(t *testing.T, env *batontest.Env, repo string) {
	t.Helper()
	hostEvent(t, env, "s1", "2031-03-04T10:05:00Z", "SessionEnd", repo, proc{})
}

// handoffProjectRunning is handoffProject with the Session still running, for
// a test that adds Events of its own before it ends.
func handoffProjectRunning(t *testing.T, env *batontest.Env) string {
	t.Helper()
	env.MakeProfileDir(".claude-personal")
	env.MakeProfileDir(".claude-work")
	env.WriteConfig(standardConfig(env) + `
[profiles.claude-work]
agent = "claude"
dir = "` + env.Path(".claude-work") + `"
`)
	repo := newRepo(t, env, "my-app")
	commitFile(t, env, repo, "README.md", "hello\n", "first commit")
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo, proc{})
	return repo
}

// commitFile writes a file into the repository and commits it.
func commitFile(t *testing.T, env *batontest.Env, repo, name, content, message string) {
	t.Helper()
	env.WriteFile(filepath.Join(repo, name), content)
	for _, args := range [][]string{{"add", "--all"}, {"commit", "--quiet", "-m", message}} {
		if res := env.Command("git", append([]string{"-C", repo}, args...)...); res.ExitCode != 0 {
			t.Fatalf("git %v: %s", args, res.Stderr)
		}
	}
}

// handoffFile is where the n-th Handoff of the test Task is written.
func handoffFile(env *batontest.Env, n string) string {
	return env.Path(".baton", "projects", "my-app", "handoffs", "2031-03-04-1", n+".md")
}

// askForNarrative ends the first message of a Handoff that has no narrative
// part: the receiving Session is asked to write it.
const askForNarrative = ". Its narrative part is missing: before you start, write it from the Facts section, the Event file and the git history, and save it with the baton-handoff skill (it pipes the Markdown to baton handoff save)"

func TestHandoff_ToAnotherClaudeProfileWritesTheHandoffAndOpensATabThatPointsToIt(t *testing.T) {
	env := newEnv(t)
	repo := handoffProject(t, env)

	res := env.Run("handoff", handoffTask, "--to", "claude@claude-work")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	path := handoffFile(env, "1")
	if !strings.Contains(env.ReadFile(path), "narrative: missing\n") {
		t.Errorf("handoff = %q, want the header to say narrative: missing", env.ReadFile(path))
	}
	want := "cd " + repo + " && env -u ANTHROPIC_API_KEY -u OPENAI_API_KEY CLAUDE_CONFIG_DIR=" + env.Path(".claude-work") +
		" claude 'continue the Task from Handoff " + path + askForNarrative + "'"
	if line := typedLine(t, env); line != want {
		t.Errorf("tab line = %q, want %q", line, want)
	}
	if !strings.Contains(res.Stdout, path) {
		t.Errorf("stdout = %q, want it to name the Handoff %s", res.Stdout, path)
	}
}

// activeSession adds a second Session of the Task, which has started and not
// ended, and returns the function that ends it the way /exit does.
func activeSession(t *testing.T, env *batontest.Env, repo string) (end func()) {
	t.Helper()
	hostEvent(t, env, "s2", "2031-03-04T11:00:00Z", "SessionStart", repo, proc{})
	return func() { hostEvent(t, env, "s2", "2031-03-04T11:30:00Z", "SessionEnd", repo, proc{}) }
}

func TestHandoff_WaitsForTheActiveSessionAndStartsTheNewOneOnlyAfterItEnded(t *testing.T) {
	env := newEnv(t)
	repo := handoffProject(t, env)
	end := activeSession(t, env, repo)

	p := env.Start("handoff", handoffTask, "--to", "claude@claude-work")
	p.WaitStdout("waiting for the claude session s2 of " + handoffTask)

	if calls := env.Calls("guake"); len(calls) != 0 {
		t.Errorf("guake was called while the Session was active: %v", calls)
	}
	if env.Exists(handoffFile(env, "1")) {
		t.Error("the Handoff was written while the Session was active")
	}

	end()
	res := p.Wait()

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(env.ReadFile(handoffFile(env, "1")), "session s2 (ended)") {
		t.Errorf("handoff = %q, want it to name the Session that ended", env.ReadFile(handoffFile(env, "1")))
	}
	if len(env.Calls("guake")) != 1 {
		t.Errorf("guake calls = %v, want one tab after the Session ended", env.Calls("guake"))
	}
}

func TestHandoff_TheAutomaticPartIsTakenAfterTheWaitSoAChangeMadeMeanwhileIsInIt(t *testing.T) {
	env := newEnv(t)
	repo := handoffProject(t, env)
	end := activeSession(t, env, repo)

	p := env.Start("handoff", handoffTask, "--to", "claude@claude-work")
	p.WaitStdout("waiting for")
	// The old Session goes on working while the command waits.
	commitFile(t, env, repo, "second.txt", "two\n", "second commit")
	env.WriteFile(filepath.Join(repo, "README.md"), "hello\nmore\n")
	env.WriteFile(filepath.Join(repo, "new.txt"), "x\n")
	end()
	res := p.Wait()

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	got := env.ReadFile(handoffFile(env, "1"))
	for _, want := range []string{
		"- Branch: main\n",
		"second commit\n",
		"```\n M README.md\n?? new.txt\n```\n",
		" README.md | 1 +\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("handoff lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "first commit") {
		t.Errorf("handoff names the first commit as HEAD:\n%s", got)
	}
}

func TestHandoff_ASessionWhoseProcessWasKilledCountsAsOverAndTheCommandContinues(t *testing.T) {
	env := newEnv(t)
	repo := handoffProject(t, env)
	agentProc, start := sleeper(t)
	live := proc{host: thisHost(t), pid: agentProc.Process.Pid, start: start}
	hostEvent(t, env, "s2", "2031-03-04T11:00:00Z", "SessionStart", repo, live)

	p := env.Start("handoff", handoffTask, "--to", "claude@claude-work")
	p.WaitStdout("waiting for")
	if len(env.Calls("guake")) != 0 {
		t.Fatal("guake was called while the Agent was running")
	}

	_ = agentProc.Process.Kill()
	_ = agentProc.Wait()
	res := p.Wait()

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(env.ReadFile(handoffFile(env, "1")), "session s2 (lost)") {
		t.Errorf("handoff = %q, want the Session to be shown as lost", env.ReadFile(handoffFile(env, "1")))
	}
	if len(env.Calls("guake")) != 1 {
		t.Errorf("guake calls = %v, want one", env.Calls("guake"))
	}
}

func TestHandoff_CtrlCWhileWaitingExitsNonZeroAndLeavesNoHandoffAndNoSession(t *testing.T) {
	env := newEnv(t)
	repo := handoffProject(t, env)
	activeSession(t, env, repo)

	p := env.Start("handoff", handoffTask, "--to", "claude@claude-work")
	p.WaitStdout("waiting for")
	res := p.Interrupt()

	if res.ExitCode != 130 {
		t.Errorf("exit code = %d, want 130", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "no Handoff was written") {
		t.Errorf("stderr = %q, want it to say that nothing was written", res.Stderr)
	}
	if env.Exists(env.Path(".baton", "projects", "my-app", "handoffs")) {
		t.Errorf("handoffs/ exists: %v", env.ListDir(env.Path(".baton", "projects", "my-app", "handoffs")))
	}
	if calls := env.Calls("guake"); len(calls) != 0 {
		t.Errorf("guake was called: %v", calls)
	}
}

func TestHandoff_ASavedDraftBecomesTheNarrativePartAndIsRemoved(t *testing.T) {
	env := newEnv(t)
	handoffProject(t, env)
	draft := env.Path(".baton", "projects", "my-app", "handoffs", "2031-03-04-1", "draft.md")
	env.WriteFile(draft, "## Goal\n\nMake login work.\n\n## Next step\n\nRun the tests.\n")

	res := env.Run("handoff", handoffTask, "--to", "claude@claude-work")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	got := env.ReadFile(handoffFile(env, "1"))
	if !strings.Contains(got, "narrative: handing-over\n") {
		t.Errorf("handoff lacks narrative: handing-over:\n%s", got)
	}
	if want := "\n## Narrative\n\n## Goal\n\nMake login work.\n\n## Next step\n\nRun the tests.\n"; !strings.HasSuffix(got, want) {
		t.Errorf("handoff does not end with the draft under its heading:\n%s", got)
	}
	if strings.Contains(got, "Not written yet") {
		t.Errorf("handoff still has the placeholder:\n%s", got)
	}
	if env.Exists(draft) {
		t.Error("draft.md is still there")
	}
	// The narrative is there, so the receiver is not asked to write one.
	if line := typedLine(t, env); strings.Contains(line, "narrative part is missing") || !strings.HasSuffix(line, "1.md'") {
		t.Errorf("tab line = %q, want the plain first message", line)
	}
}

func TestHandoff_HandoffsOfATaskAreNumberedAndAreOwnerOnlyFilesWithNoLeftovers(t *testing.T) {
	env := newEnv(t)
	handoffProject(t, env)

	first := env.Run("handoff", handoffTask, "--to", "claude@claude-work")
	second := env.Run("handoff", handoffTask, "--to", "claude@claude-work")

	if first.ExitCode != 0 || second.ExitCode != 0 {
		t.Fatalf("exit codes = %d, %d; stderr=%q, %q", first.ExitCode, second.ExitCode, first.Stderr, second.Stderr)
	}
	for _, n := range []string{"1", "2"} {
		if mode := env.Mode(handoffFile(env, n)); mode != 0o600 {
			t.Errorf("mode of %s.md = %o, want 600", n, mode)
		}
		if !strings.Contains(env.ReadFile(handoffFile(env, n)), "handoff: "+n+"\n") {
			t.Errorf("%s.md does not say that it is Handoff %s", n, n)
		}
	}
	for _, name := range env.ListDir(filepath.Dir(handoffFile(env, "1"))) {
		if strings.Contains(name, ".tmp") {
			t.Errorf("a temporary file is left: %s", name)
		}
	}
}

// toolEvent appends a finished Bash command of the Claude Session s1 to its
// Event file.
func toolEvent(t *testing.T, env *batontest.Env, repo, ts, event, command string, extra map[string]any) {
	t.Helper()
	payload := map[string]any{"hook_event_name": event, "tool_name": "Bash", "tool_input": map[string]any{"command": command}}
	for k, v := range extra {
		payload[k] = v
	}
	writeProcEventPayload(t, env, ".baton/sessions/claude-s1.jsonl", "host", "claude-personal", "s1", ts, event, repo, proc{}, payload)
}

func TestHandoff_TheCommandsComeFromTheEventsWithTheirExitCodes(t *testing.T) {
	env := newEnv(t)
	repo := handoffProjectRunning(t, env)
	toolEvent(t, env, repo, "2031-03-04T10:01:00Z", "PostToolUse", "go build ./...",
		map[string]any{"tool_response": map[string]any{"stdout": "", "stderr": "", "interrupted": false, "isImage": false}})
	toolEvent(t, env, repo, "2031-03-04T10:02:00Z", "PostToolUseFailure", "go test ./...",
		map[string]any{"error": "Exit code 2\nFAIL: TestLogin", "is_interrupt": false})
	toolEvent(t, env, repo, "2031-03-04T10:03:00Z", "PostToolUseFailure", "sleep 600",
		map[string]any{"error": "Interrupted by user", "is_interrupt": true})
	toolEvent(t, env, repo, "2031-03-04T10:04:00Z", "PreToolUse", "never finished", nil)
	writeProcEventPayload(t, env, ".baton/sessions/claude-s1.jsonl", "host", "claude-personal", "s1", "2031-03-04T10:04:30Z", "PostToolUse", repo, proc{},
		map[string]any{"tool_name": "Write", "tool_input": map[string]any{"file_path": "/x"}})
	endFirstSession(t, env, repo)

	res := env.Run("handoff", handoffTask, "--to", "claude@claude-work")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	got := env.ReadFile(handoffFile(env, "1"))
	want := "1. exit 0, 2031-03-04T10:01:00Z\n\n```\ngo build ./...\n```\n\n" +
		"2. exit 2, 2031-03-04T10:02:00Z\n\n```\ngo test ./...\n```\n\n" +
		"3. exit ?, 2031-03-04T10:03:00Z\n\n```\nsleep 600\n```\n"
	if !strings.Contains(got, want) {
		t.Errorf("handoff lacks the commands:\n%s\nwant it to contain:\n%s", got, want)
	}
	if strings.Contains(got, "never finished") || strings.Contains(got, "/x") {
		t.Errorf("handoff lists something that is not a finished command:\n%s", got)
	}
}

func TestHandoff_OnlyTheLastTenCommandsAreListed(t *testing.T) {
	env := newEnv(t)
	repo := handoffProjectRunning(t, env)
	for i := 1; i <= 12; i++ {
		ts := "2031-03-04T10:" + strconv.Itoa(10+i) + ":00Z"
		toolEvent(t, env, repo, ts, "PostToolUse", "step-"+strconv.Itoa(i), nil)
	}
	endFirstSession(t, env, repo)

	env.Run("handoff", handoffTask, "--to", "claude@claude-work")

	got := env.ReadFile(handoffFile(env, "1"))
	for i := 1; i <= 12; i++ {
		has := strings.Contains(got, "\nstep-"+strconv.Itoa(i)+"\n")
		if want := i >= 3; has != want {
			t.Errorf("step-%d listed = %v, want %v", i, has, want)
		}
	}
}

func TestHandoff_ACommandStringIsKeptAsRecordedAndALongOneIsCut(t *testing.T) {
	env := newEnv(t)
	repo := handoffProjectRunning(t, env)
	toolEvent(t, env, repo, "2031-03-04T10:01:00Z", "PostToolUse", "curl -H 'Authorization: Bearer abc123' https://example.invalid", nil)
	toolEvent(t, env, repo, "2031-03-04T10:02:00Z", "PostToolUse", "echo "+strings.Repeat("x", 5000), nil)
	endFirstSession(t, env, repo)

	env.Run("handoff", handoffTask, "--to", "claude@claude-work")

	got := env.ReadFile(handoffFile(env, "1"))
	if !strings.Contains(got, "Bearer abc123") {
		t.Errorf("the command was not kept as recorded:\n%s", got)
	}
	if strings.Contains(got, strings.Repeat("x", 1500)) || !strings.Contains(got, "more bytes not shown") {
		t.Errorf("the long command was not cut")
	}
}

// codexSession plays a Codex Session in the sandbox of Project app that ran one
// command and ended, started with `baton new`. It returns the id of its Task.
func codexSession(t *testing.T, env *batontest.Env) string {
	t.Helper()
	if res := env.Run("new", "app", "--agent", "codex"); res.ExitCode != 0 {
		t.Fatalf("new: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	root := containerRoot(t, env, "app")
	for _, event := range []string{"SessionStart", "PostToolUse", "SessionEnd"} {
		codexHook(t, env, root, event, "thr_123", "/workspace")
	}
	tasks := env.ListDir(env.Path(".baton", "projects", "app", "tasks"))
	if len(tasks) != 1 {
		t.Fatalf("tasks = %v, want one", tasks)
	}
	return strings.TrimSuffix(tasks[0], ".toml")
}

func TestHandoff_ToCodexInTheSandboxTheFirstMessageNamesTheHandoffAsTheContainerSeesIt(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	task := codexSession(t, env)

	res := env.Run("handoff", "app/"+task, "--to", "codex")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	file := env.Path(".baton", "projects", "app", "handoffs", task, "1.md")
	if !strings.Contains(res.Stdout, file) {
		t.Errorf("stdout = %q, want the host path %s", res.Stdout, file)
	}
	got := env.ReadFile(file)
	if !strings.Contains(got, "1. exit ?, ") || !strings.Contains(got, "\ngo test ./...\n") {
		t.Errorf("handoff lacks the Codex command with an unknown exit code:\n%s", got)
	}
	if !strings.Contains(got, "- Handed over to: codex, profile app.codex, runtime sandbox\n") {
		t.Errorf("handoff does not say where the Task goes:\n%s", got)
	}

	// The second tab is the new Session; the first is the one `baton new` opened.
	calls := guakeArgs(env)
	if len(calls) != 2 {
		t.Fatalf("guake calls = %q, want two", calls)
	}
	line := calls[1][1]
	if res := env.Command("sh", "-c", line); res.ExitCode != 0 {
		t.Fatalf("the tab line %q failed: %s", line, res.Stderr)
	}
	runs := dockerCalls(env, "run")
	last := runs[len(runs)-1]
	wantTail := []string{imageTag, "env", "-u", "ANTHROPIC_API_KEY", "-u", "OPENAI_API_KEY", "codex",
		"continue the Task from Handoff /baton/project/handoffs/" + task + "/1.md" + askForNarrative}
	if got := last[len(last)-len(wantTail):]; strings.Join(got, "\n") != strings.Join(wantTail, "\n") {
		t.Errorf("end of the docker command = %q, want %q", got, wantTail)
	}
}

func TestHandoff_ToAWorkingCopyOtherThanTheMainCheckoutOfASandboxProjectIsRefused(t *testing.T) {
	env := newEnv(t)
	repo, _ := launchProject(t, env)
	commitFile(t, env, repo, "README.md", "hello\n", "first commit")
	worktree := env.Path("work", "app-wt")
	if res := env.Command("git", "-C", repo, "worktree", "add", "--quiet", worktree); res.ExitCode != 0 {
		t.Fatalf("git worktree add: %s", res.Stderr)
	}
	// A Claude Session started by hand on the host, in the worktree.
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", worktree, proc{})
	hostEvent(t, env, "s1", "2031-03-04T10:05:00Z", "SessionEnd", worktree, proc{})

	res := env.Run("handoff", "app/2031-03-04-1", "--to", "codex")

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "whose Sessions run only in the main checkout "+repo+", not in the worktree "+worktree) {
		t.Errorf("exit code = %d, stderr=%q, want a refusal that names the main checkout and the worktree", res.ExitCode, res.Stderr)
	}
	if env.Exists(env.Path(".baton", "projects", "app", "handoffs")) {
		t.Error("a Handoff was written")
	}
	if calls := env.Calls("guake"); len(calls) != 0 {
		t.Errorf("guake was called: %v", calls)
	}
}

func TestHandoff_ARefusedRequestNeitherWaitsNorWritesNorOpensATab(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown task", []string{"handoff", "my-app/2031-03-04-9", "--to", "claude@claude-work"}, "project my-app has no task 2031-03-04-9"},
		{"unknown project", []string{"handoff", "nowhere/x", "--to", "claude"}, `unknown project "nowhere"`},
		{"codex on the host", []string{"handoff", handoffTask, "--to", "codex"}, "codex runs only in a sandbox"},
		{"unknown profile", []string{"handoff", handoffTask, "--to", "claude@nobody"}, `unknown profile "nobody"`},
		{"several profiles and no choice", []string{"handoff", handoffTask, "--to", "claude"}, "several claude profiles"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newEnv(t)
			repo := handoffProject(t, env)
			activeSession(t, env, repo) // a refusal must not wait for this

			res := env.Run(tc.args...)

			if res.ExitCode != 1 || !strings.Contains(res.Stderr, tc.want) {
				t.Errorf("exit code = %d, stderr=%q, want exit 1 and %q", res.ExitCode, res.Stderr, tc.want)
			}
			if env.Exists(env.Path(".baton", "projects", "my-app", "handoffs")) || len(env.Calls("guake")) != 0 {
				t.Error("a Handoff was written or a tab opened")
			}
		})
	}
}

func TestHandoff_UsageErrorsExitWithTwo(t *testing.T) {
	for _, args := range [][]string{
		{"handoff"},
		{"handoff", handoffTask},
		{"handoff", "--to", "claude"},
		{"handoff", "my-app", "--to", "claude"},
		{"handoff", handoffTask, "--to", "gemini"},
		{"handoff", handoffTask, "extra", "--to", "claude"},
	} {
		env := newEnv(t)
		res := env.Run(args...)
		if res.ExitCode != 2 || !strings.Contains(res.Stderr, "usage: baton handoff <project>/<task> --to") {
			t.Errorf("%v: exit code = %d, stderr=%q, want a usage error", args, res.ExitCode, res.Stderr)
		}
	}
}

func TestHandoff_ATaskThatHasNoSessionYetHasNothingToHandOver(t *testing.T) {
	env := newEnv(t)
	handoffProject(t, env)
	other := newRepo(t, env, "other")
	// Makes a Task, and opens a tab, but no Event has been reported.
	if res := env.Run("new", other, "login-fix", "--agent", "claude", "--profile", "claude-work"); res.ExitCode != 0 {
		t.Fatalf("new: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	res := env.Run("handoff", "other/login-fix", "--to", "claude@claude-personal")

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "task other/login-fix has no Session to hand over") {
		t.Errorf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if len(env.Calls("guake")) != 1 {
		t.Errorf("guake calls = %v, want only the tab of baton new", env.Calls("guake"))
	}
}

func TestHandoff_ANamedTasksHandoffsAreKeptUnderItsIdSoRenamingItBreaksNothing(t *testing.T) {
	env := newEnv(t)
	repo := handoffProject(t, env)
	// The Task made now is current from now on, so the Session (dated later)
	// belongs to it.
	if res := env.Run("new", repo, "login-fix", "--agent", "claude", "--profile", "claude-work"); res.ExitCode != 0 {
		t.Fatalf("new: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	res := env.Run("handoff", "my-app/login-fix", "--to", "claude@claude-personal")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	dirs := env.ListDir(env.Path(".baton", "projects", "my-app", "handoffs"))
	if len(dirs) != 1 || stable(dirs[0]) != "YYYY-MM-DD-1" {
		t.Errorf("handoffs/ = %v, want one directory named after the Task's id", dirs)
	}
	if got := env.ReadFile(env.Path(".baton", "projects", "my-app", "handoffs", dirs[0], "1.md")); !strings.Contains(got, "- Task name: login-fix\n") {
		t.Errorf("handoff does not name the Task:\n%s", got)
	}
}
