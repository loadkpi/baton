package test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/loadkpi/baton/internal/batontest"
)

// writeEvent appends an Event with a time of the test's choosing to the Event
// file of a Claude Session, the way `baton hook` would have written it. Tests
// that need a known date (generated Task names carry the date of the Event)
// use it instead of the hook, which stamps the wall clock.
func writeEvent(t *testing.T, env *batontest.Env, sessionID, ts, event, cwd string) {
	t.Helper()
	line, err := json.Marshal(map[string]any{
		"v": 1, "ts": ts, "agent": "claude", "profile": "claude-personal", "runtime": "host",
		"host": "test", "agent_pid": 1, "agent_start": 1, "session_id": sessionID,
		"event": event, "cwd": cwd, "payload": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := ".baton/sessions/claude-" + sessionID + ".jsonl"
	existing := ""
	if env.Exists(path) {
		existing = env.ReadFile(path)
	}
	env.WriteFile(path, existing+string(line)+"\n")
}

// generatedName matches the name of an unnamed Task, <date>-<n>.
var generatedName = regexp.MustCompile(`\d{4}-\d{2}-\d{2}-(\d+)`)

// stable replaces the date of generated Task names with YYYY-MM-DD, which is
// as wide, because the date of a Task made by a command is the day of the test
// run.
func stable(s string) string {
	return generatedName.ReplaceAllString(s, "YYYY-MM-DD-$1")
}

func TestTask_ASessionInAWorkingCopyWithoutATaskGetsAnUnnamedTaskNamedAfterTheDayOfItsFirstEvent(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	writeEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo)
	writeEvent(t, env, "s1", "2031-03-04T10:05:00Z", "Stop", repo)

	rows := lsRowsRaw(t, env)

	want := [][]string{{"my-app", "2031-03-04-1", "claude", "claude-personal", "host", "waiting_input"}}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows = %v, want %v", rows, want)
	}
	if got, want := env.ReadFile(".baton/projects/my-app/tasks/2031-03-04-1.toml"), "status = \"open\"\ncreated = \"2031-03-04T10:00:00Z\"\n"; got != want {
		t.Errorf("task record = %q, want %q", got, want)
	}
	journal := `{"v":1,"ts":"2031-03-04T10:00:00Z","working_copy":"` + repo + `","task":"2031-03-04-1"}` + "\n"
	if got := env.ReadFile(".baton/projects/my-app/current.jsonl"); got != journal {
		t.Errorf("current.jsonl = %q, want %q", got, journal)
	}
}

// journal returns the lines of a Project's current.jsonl as documents.
func journal(t *testing.T, env *batontest.Env, project string) []map[string]any {
	t.Helper()
	return eventLines(t, env, ".baton/projects/"+project+"/current.jsonl")
}

func TestTask_NewWithoutANameMakesAnUnnamedTaskCurrentForTheWorkingCopy(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")

	res := env.Run("new", repo, "--agent", "claude")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got, want := stable(guakeTitle(t, env)), "my-app/YYYY-MM-DD-1:claude"; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}
	lines := journal(t, env, "my-app")
	if len(lines) != 1 {
		t.Fatalf("journal = %v, want one line", lines)
	}
	if lines[0]["working_copy"] != repo || !generatedName.MatchString(lines[0]["task"].(string)) || lines[0]["v"] != float64(1) {
		t.Errorf("journal line = %v, want Working copy %s and a generated Task", lines[0], repo)
	}
	if _, err := time.Parse(time.RFC3339Nano, lines[0]["ts"].(string)); err != nil {
		t.Errorf("ts = %v: %v", lines[0]["ts"], err)
	}
	id := lines[0]["task"].(string)
	record := env.ReadFile(".baton/projects/my-app/tasks/" + id + ".toml")
	if !strings.HasPrefix(record, "status = \"open\"\ncreated = \"") {
		t.Errorf("task record = %q, want an open Task with a creation time", record)
	}
}

func TestTask_NewWithANameMakesANamedTaskAndTheSameNameContinuesIt(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")

	first := env.Run("new", repo, "login-fix", "--agent", "claude")
	second := env.Run("new", "my-app", "login-fix", "--agent", "claude")

	if first.ExitCode != 0 || second.ExitCode != 0 {
		t.Fatalf("exit codes = %d, %d; stderr=%q, %q", first.ExitCode, second.ExitCode, first.Stderr, second.Stderr)
	}
	lines := journal(t, env, "my-app")
	if len(lines) != 2 || lines[0]["task"] != lines[1]["task"] || lines[0]["working_copy"] != repo {
		t.Fatalf("journal = %v, want two lines for one Task in %s", lines, repo)
	}
	id := lines[0]["task"].(string)
	if got := stable(id); got != "YYYY-MM-DD-1" {
		t.Errorf("task id = %q, want the first of the day", id)
	}
	if got, want := stable(env.ReadFile(".baton/projects/my-app/tasks/"+id+".toml")), "name = \"login-fix\"\nstatus = \"open\"\ncreated = \""; !strings.HasPrefix(got, want) {
		t.Errorf("task record = %q, want it to start with %q", got, want)
	}
	if got := env.ListDir(".baton/projects/my-app/tasks"); !reflect.DeepEqual(got, []string{id + ".toml"}) {
		t.Errorf("tasks = %v, want one record", got)
	}
}

func TestTask_AnUnnamedTaskCanBeContinuedByItsId(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	env.Run("new", repo, "--agent", "claude")
	id := journal(t, env, "my-app")[0]["task"].(string)

	res := env.Run("new", "my-app", id, "--agent", "claude")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if lines := journal(t, env, "my-app"); len(lines) != 2 || lines[1]["task"] != id {
		t.Errorf("journal = %v, want the second line to continue %s", lines, id)
	}
	if got := guakeArgs(env)[1][3]; got != "my-app/"+id+":claude" {
		t.Errorf("title = %q", got)
	}
}

func TestTask_AnIdNoTaskHasIsRefusedWithoutOpeningATab(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")

	res := env.Run("new", repo, "2031-03-04-7", "--agent", "claude")

	if want := "baton: project my-app has no task 2031-03-04-7\n"; res.ExitCode != 1 || res.Stderr != want {
		t.Errorf("exit code = %d, stderr=%q, want %q", res.ExitCode, res.Stderr, want)
	}
	if calls := env.Calls("guake"); len(calls) != 0 {
		t.Errorf("guake was called: %v", calls)
	}
}

func TestTask_AWorktreeHasItsOwnCurrentTaskOfTheSameProject(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	wt := addWorktree(t, env, repo, "my-app-feature")

	env.Run("new", repo, "main-work", "--agent", "claude")
	env.Run("new", wt, "feature-work", "--agent", "claude")

	lines := journal(t, env, "my-app")
	if len(lines) != 2 || lines[0]["working_copy"] != repo || lines[1]["working_copy"] != wt || lines[0]["task"] == lines[1]["task"] {
		t.Fatalf("journal = %v, want one Task per Working copy (%s, %s)", lines, repo, wt)
	}
	calls := guakeArgs(env)
	if len(calls) != 2 || !strings.HasPrefix(calls[1][1], "cd "+wt+" && ") || calls[1][3] != "my-app/feature-work:claude" {
		t.Errorf("guake calls = %q, want the second tab in the worktree", calls)
	}
}

// tasksInLs returns the Task column of `baton ls`, one cell per Session.
func tasksInLs(t *testing.T, env *batontest.Env) []string {
	t.Helper()
	var tasks []string
	for _, row := range lsRows(t, env) {
		tasks = append(tasks, row[1])
	}
	return tasks
}

func TestTask_ASessionStartedByHandBelongsToTheCurrentTaskOfItsWorkingCopy(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	env.Run("new", repo, "login-fix", "--agent", "claude")

	runHook(t, env, "claude-work", "SessionStart", "by-hand", repo)

	if got, want := tasksInLs(t, env), []string{"login-fix"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tasks = %v, want %v", got, want)
	}
	if got := env.ListDir(".baton/projects/my-app/tasks"); len(got) != 1 {
		t.Errorf("tasks = %v, want no new Task", got)
	}
}

func TestTask_ASessionKeepsTheTaskThatWasCurrentAtItsFirstEventWhenItIsReadOnlyAfterBatonNewSwitchedTasks(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	env.Run("new", repo, "task-a", "--agent", "claude")
	runHook(t, env, "claude-personal", "SessionStart", "s1", repo)
	runHook(t, env, "claude-personal", "UserPromptSubmit", "s1", repo)
	runHook(t, env, "claude-personal", "SessionEnd", "s1", repo)
	switched := env.Run("new", repo, "task-b", "--agent", "claude")
	if switched.ExitCode != 0 {
		t.Fatalf("new task-b: exit code = %d, stderr=%q", switched.ExitCode, switched.Stderr)
	}

	// Nothing has read the Session until now.
	got := tasksInLs(t, env)

	if want := []string{"task-a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tasks = %v, want %v", got, want)
	}
}

func TestTask_ASessionAfterTheSwitchBelongsToTheNewTask(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	env.Run("new", repo, "task-a", "--agent", "claude")
	runHook(t, env, "claude-personal", "SessionStart", "s1", repo)
	runHook(t, env, "claude-personal", "SessionEnd", "s1", repo)
	env.Run("new", repo, "task-b", "--agent", "claude")
	runHook(t, env, "claude-personal", "SessionStart", "s2", repo)

	got := tasksInLs(t, env)

	if want := []string{"task-b", "task-a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tasks = %v, want %v (most recent Session first)", got, want)
	}
}

func TestTask_ASessionAfterClearBelongsToTheSameTask(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	env.Run("new", repo, "login-fix", "--agent", "claude")
	// /clear ends the conversation and starts another one in the same tab.
	runHook(t, env, "claude-personal", "SessionStart", "before-clear", repo)
	runHook(t, env, "claude-personal", "SessionEnd", "before-clear", repo)
	runHook(t, env, "claude-personal", "SessionStart", "after-clear", repo)

	got := tasksInLs(t, env)

	if want := []string{"login-fix", "login-fix"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tasks = %v, want %v", got, want)
	}
}

func TestTask_SessionsWithoutATaskMakeOneUnnamedTaskPerWorkingCopyEvenWhenTheyAreReadOutOfOrder(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	wt := addWorktree(t, env, repo, "my-app-feature")
	// Most recent first in ls is the reverse of their start: the later Session of a
	// Working copy must find the Task of the earlier one, not make its own.
	writeEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo)
	writeEvent(t, env, "s1", "2031-03-04T10:30:00Z", "SessionEnd", repo)
	writeEvent(t, env, "s2", "2031-03-04T11:00:00Z", "SessionStart", repo)
	writeEvent(t, env, "s3", "2031-03-04T10:15:00Z", "SessionStart", wt)

	rows := lsRowsRaw(t, env)

	var got []string
	for _, row := range rows {
		got = append(got, row[1])
	}
	// s2 (11:00), s1 (10:30, ended), s3 (10:15): s1 and s2 share the Task of repo.
	if want := []string{"2031-03-04-1", "2031-03-04-1", "2031-03-04-2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tasks = %v, want %v", got, want)
	}
}

func TestTask_ASessionOutsideGitHasNoTask(t *testing.T) {
	env := newEnv(t)
	writeEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", env.Path("notes"))

	rows := lsRowsRaw(t, env)

	if want := [][]string{{"-", "-", "claude", "claude-personal", "host", "waiting_input"}}; !reflect.DeepEqual(rows, want) {
		t.Errorf("rows = %v, want %v", rows, want)
	}
}

func TestTask_NewRefusesWhileTheWorkingCopyHasAnActiveSessionAndNamesIt(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	writeEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo)
	writeEvent(t, env, "s1", "2031-03-04T10:30:00Z", "Stop", filepath.Join(repo, "pkg"))

	res := env.Run("new", repo, "other", "--agent", "claude")

	want := "baton: working copy " + repo + " of project my-app already has an active session: " +
		"claude, profile claude-personal, session s1, last event 2031-03-04T10:30:00Z (waiting_input); " +
		"end it, or hand the Task over with baton handoff\n"
	if res.ExitCode != 1 || res.Stderr != want {
		t.Errorf("exit code = %d, stderr=%q, want %q", res.ExitCode, res.Stderr, want)
	}
	if calls := env.Calls("guake"); len(calls) != 0 {
		t.Errorf("guake was called: %v", calls)
	}
	// The current Task did not change, nor was a Task made.
	if env.Exists(".baton/projects/my-app/current.jsonl") || env.Exists(".baton/projects/my-app/tasks") {
		t.Errorf("the refused command left files: %v", env.ListDir(".baton/projects/my-app"))
	}
}

func TestTask_ASessionThatEndedDoesNotBlockTheWorkingCopy(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	writeEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo)
	writeEvent(t, env, "s1", "2031-03-04T10:30:00Z", "SessionEnd", repo)

	res := env.Run("new", repo, "other", "--agent", "claude")

	if res.ExitCode != 0 {
		t.Errorf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
}

func TestTask_OnlySessionsOfTheSameWorkingCopyBlock(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	wt := addWorktree(t, env, repo, "my-app-feature")
	other := newRepo(t, env, "other-app")
	writeEvent(t, env, "in-worktree", "2031-03-04T10:00:00Z", "SessionStart", wt)
	writeEvent(t, env, "in-other-project", "2031-03-04T10:00:00Z", "SessionStart", other)

	main := env.Run("new", repo, "--agent", "claude")
	blocked := env.Run("new", wt, "--agent", "claude")

	if main.ExitCode != 0 {
		t.Errorf("main checkout: exit code = %d, stderr=%q, want it free", main.ExitCode, main.Stderr)
	}
	if blocked.ExitCode != 1 || !strings.Contains(blocked.Stderr, "session in-worktree") {
		t.Errorf("worktree: exit code = %d, stderr=%q, want it blocked by in-worktree", blocked.ExitCode, blocked.Stderr)
	}
}

func TestTask_TheCurrentTaskDoesNotChangeWhileASessionIsActive(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	env.Run("new", repo, "task-a", "--agent", "claude")
	runHook(t, env, "claude-personal", "SessionStart", "s1", repo)

	refused := env.Run("new", repo, "task-b", "--agent", "claude")
	runHook(t, env, "claude-personal", "SessionStart", "s2", repo) // another hand-started Session

	if refused.ExitCode != 1 {
		t.Errorf("new task-b: exit code = %d, stderr=%q, want it refused", refused.ExitCode, refused.Stderr)
	}
	if lines := journal(t, env, "my-app"); len(lines) != 1 {
		t.Errorf("journal = %v, want the one line of task-a", lines)
	}
	if got, want := tasksInLs(t, env), []string{"task-a", "task-a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tasks = %v, want %v", got, want)
	}
	if got := env.ListDir(".baton/projects/my-app/tasks"); len(got) != 1 {
		t.Errorf("tasks = %v, want no task-b", got)
	}
}

// unnamedTask makes an unnamed Task in a new repository registered as my-app
// and returns its id.
func unnamedTask(t *testing.T, env *batontest.Env) string {
	t.Helper()
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	if res := env.Run("new", repo, "--agent", "claude"); res.ExitCode != 0 {
		t.Fatalf("new: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	return journal(t, env, "my-app")[0]["task"].(string)
}

func TestTask_AnUnnamedTaskCanBeNamedLaterAndEverySessionOfItShowsTheName(t *testing.T) {
	env := newEnv(t)
	id := unnamedTask(t, env)
	runHook(t, env, "claude-personal", "SessionStart", "s1", env.Path("work", "my-app"))

	res := env.Run("task", "name", "my-app/"+id, "login-fix")

	if want := "my-app/login-fix\n"; res.ExitCode != 0 || res.Stdout != want {
		t.Errorf("exit code = %d, stdout=%q, stderr=%q, want %q", res.ExitCode, res.Stdout, res.Stderr, want)
	}
	if got, want := tasksInLs(t, env), []string{"login-fix"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tasks = %v, want %v", got, want)
	}
	if record := env.ReadFile(".baton/projects/my-app/tasks/" + id + ".toml"); !strings.HasPrefix(record, "name = \"login-fix\"\nstatus = \"open\"\n") {
		t.Errorf("task record = %q", record)
	}
	// The name now finds the same Task.
	if blocked := env.Run("new", "my-app", "login-fix", "--agent", "claude"); blocked.ExitCode != 1 {
		t.Fatalf("new while s1 is active: exit code = %d, want it refused", blocked.ExitCode)
	}
	runHook(t, env, "claude-personal", "SessionEnd", "s1", env.Path("work", "my-app"))
	if again := env.Run("new", "my-app", "login-fix", "--agent", "claude"); again.ExitCode != 0 {
		t.Fatalf("new: exit code = %d, stderr=%q", again.ExitCode, again.Stderr)
	}
	if got := env.ListDir(".baton/projects/my-app/tasks"); !reflect.DeepEqual(got, []string{id + ".toml"}) {
		t.Errorf("tasks = %v, want only %s", got, id)
	}
}

func TestTask_ANamedTaskCanBeRenamed(t *testing.T) {
	env := newEnv(t)
	unnamedTask(t, env)
	env.Run("task", "name", "my-app/"+journal(t, env, "my-app")[0]["task"].(string), "login-fix")

	res := env.Run("task", "name", "my-app/login-fix", "auth-fix")

	if res.ExitCode != 0 || res.Stdout != "my-app/auth-fix\n" {
		t.Errorf("exit code = %d, stdout=%q, stderr=%q", res.ExitCode, res.Stdout, res.Stderr)
	}
}

func TestTask_NamingIsRefusedForABadNameAnUnknownTaskAndANameInUse(t *testing.T) {
	env := newEnv(t)
	id := unnamedTask(t, env)
	env.Run("new", "my-app", "taken", "--agent", "claude")
	tests := []struct {
		name     string
		args     []string
		exitCode int
		stderr   string
	}{
		{"name that looks like an id", []string{"task", "name", "my-app/" + id, "2031-03-04-1"}, 2, "baton: task name: invalid task name \"2031-03-04-1\": it looks like a generated task id (<date>-<n>)\n"},
		{"bad name", []string{"task", "name", "my-app/" + id, "Not A Name"}, 2, "baton: task name: invalid task name \"Not A Name\": use lowercase letters, digits, '.', '_' and '-', starting with a letter or digit\n"},
		{"unknown task", []string{"task", "name", "my-app/ghost", "new-name"}, 1, "baton: project my-app has no task ghost\n"},
		{"unknown project", []string{"task", "name", "nowhere/" + id, "new-name"}, 1, "baton: unknown project \"nowhere\"; pass the path of a git repository to register it\n"},
		{"name in use", []string{"task", "name", "my-app/" + id, "taken"}, 1, "baton: project my-app already has a task named taken\n"},
		{"not project/task", []string{"task", "name", id, "new-name"}, 2, "baton: task name: want <project>/<task>, got \"" + id + "\" (usage: baton task name <project>/<task> <name>)\n"},
		{"missing name", []string{"task", "name", "my-app/" + id}, 2, "baton: task name: a new name is required (usage: baton task name <project>/<task> <name>)\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := env.Run(tt.args...)

			if res.ExitCode != tt.exitCode || res.Stderr != tt.stderr {
				t.Errorf("exit code = %d, stderr=%q, want %d and %q", res.ExitCode, res.Stderr, tt.exitCode, tt.stderr)
			}
		})
	}
	if record := env.ReadFile(".baton/projects/my-app/tasks/" + id + ".toml"); strings.Contains(record, "name") {
		t.Errorf("task record = %q, want it still unnamed", record)
	}
}

func TestTask_StatusIsSetToOpenDoneOrDropped(t *testing.T) {
	env := newEnv(t)
	id := unnamedTask(t, env)
	env.Run("task", "name", "my-app/"+id, "login-fix")

	for _, status := range []string{"done", "dropped", "open"} {
		res := env.Run("task", "status", "my-app/login-fix", status)

		if res.ExitCode != 0 || res.Stdout != "my-app/login-fix "+status+"\n" {
			t.Errorf("%s: exit code = %d, stdout=%q, stderr=%q", status, res.ExitCode, res.Stdout, res.Stderr)
		}
		if record := env.ReadFile(".baton/projects/my-app/tasks/" + id + ".toml"); !strings.Contains(record, "status = \""+status+"\"\n") || !strings.HasPrefix(record, "name = \"login-fix\"\n") {
			t.Errorf("%s: task record = %q", status, record)
		}
	}
	// By id as well.
	if res := env.Run("task", "status", "my-app/"+id, "done"); res.ExitCode != 0 {
		t.Errorf("by id: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
}

func TestTask_AStatusThatIsNotOneOfTheThreeIsRefused(t *testing.T) {
	env := newEnv(t)
	id := unnamedTask(t, env)

	res := env.Run("task", "status", "my-app/"+id, "running")

	want := "baton: task status: unknown task status \"running\" (known: open, done, dropped) (usage: baton task status <project>/<task> <open|done|dropped>)\n"
	if res.ExitCode != 2 || res.Stderr != want {
		t.Errorf("exit code = %d, stderr=%q, want 2 and %q", res.ExitCode, res.Stderr, want)
	}
}

func TestTask_ATaskCommandNeedsASubcommand(t *testing.T) {
	env := newEnv(t)

	for _, args := range [][]string{{"task"}, {"task", "rename"}} {
		res := env.Run(args...)

		if res.ExitCode != 2 || !strings.HasPrefix(res.Stderr, "baton: task: ") || !strings.Contains(res.Stderr, "(available: name, status)") {
			t.Errorf("%v: exit code = %d, stderr=%q", args, res.ExitCode, res.Stderr)
		}
	}
}

func TestTask_ParallelReadersMakeOneTaskForSessionsWithoutOne(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	writeEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo)
	writeEvent(t, env, "s2", "2031-03-04T11:00:00Z", "SessionStart", repo)
	writeEvent(t, env, "s3", "2031-03-04T12:00:00Z", "SessionStart", repo)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if res := env.Run("ls"); res.ExitCode != 0 {
				t.Errorf("ls: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
			}
		}()
	}
	wg.Wait()

	if got := env.ListDir(".baton/projects/my-app/tasks"); !reflect.DeepEqual(got, []string{"2031-03-04-1.toml"}) {
		t.Errorf("tasks = %v, want one", got)
	}
	if lines := journal(t, env, "my-app"); len(lines) != 1 {
		t.Errorf("journal = %v, want one line", lines)
	}
}

func TestTask_ParallelNewCommandsGetDifferentTasksAndAWholeJournal(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	if res := env.Run("new", repo, "--agent", "claude"); res.ExitCode != 0 { // registers the Project
		t.Fatalf("new: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	const parallel = 6
	var wg sync.WaitGroup
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if res := env.Run("new", "my-app", "--agent", "claude"); res.ExitCode != 0 {
				t.Errorf("new: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
			}
		}()
	}
	wg.Wait()

	lines := journal(t, env, "my-app")
	ids := make(map[string]bool)
	for _, line := range lines {
		ids[line["task"].(string)] = true
	}
	if len(lines) != parallel+1 || len(ids) != parallel+1 {
		t.Errorf("journal has %d lines for %d different Tasks, want %d of each", len(lines), len(ids), parallel+1)
	}
	if got := len(env.ListDir(".baton/projects/my-app/tasks")); got != parallel+1 {
		t.Errorf("%d task records, want %d", got, parallel+1)
	}
}
