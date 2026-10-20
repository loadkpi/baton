package test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/loadkpi/baton/internal/batontest"
)

// newRepo creates a git repository at ~/work/<name> and returns its path.
func newRepo(t *testing.T, env *batontest.Env, name string) string {
	t.Helper()
	dir := env.Path("work", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if res := env.Command("git", "-C", dir, "init", "--quiet", "--initial-branch=main"); res.ExitCode != 0 {
		t.Fatalf("git init: %s", res.Stderr)
	}
	return dir
}

// lsRows runs `baton ls` and returns the lines under the header as cells.
func lsRows(t *testing.T, env *batontest.Env) [][]string {
	t.Helper()
	res := env.Run("ls")
	if res.ExitCode != 0 {
		t.Fatalf("ls: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) == 0 || strings.Fields(lines[0])[0] != "PROJECT" {
		t.Fatalf("ls output has no header: %q", res.Stdout)
	}
	var rows [][]string
	for _, line := range lines[1:] {
		rows = append(rows, strings.Fields(line))
	}
	return rows
}

func TestLs_ShowsAHostClaudeSessionWithItsProjectProfileRuntimeAndStatus(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	runHook(t, env, "claude-personal", "SessionStart", "abc123", repo)

	res := env.Run("ls")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := "PROJECT  AGENT   PROFILE          RUNTIME  STATUS\n" +
		"my-app   claude  claude-personal  host     waiting_input\n"
	if res.Stdout != want {
		t.Errorf("stdout = %q, want %q", res.Stdout, want)
	}
}

func TestLs_NoSessionsSaysSo(t *testing.T) {
	env := newEnv(t)

	res := env.Run("ls")

	if res.ExitCode != 0 || res.Stdout != "no sessions\n" {
		t.Errorf("exit code = %d, stdout=%q, stderr=%q", res.ExitCode, res.Stdout, res.Stderr)
	}
}

func TestLs_StatusFollowsTheLastEventPerTheStatusTable(t *testing.T) {
	tests := []struct {
		event  string
		status string
	}{
		{"SessionStart", "waiting_input"},
		{"UserPromptSubmit", "running"},
		{"PreToolUse", "running"},
		{"PostToolUse", "running"},
		{"PostToolUseFailure", "running"},
		{"PermissionRequest", "waiting_approval"},
		{"Stop", "waiting_input"},
		{"SessionEnd", "ended"},
	}
	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			env := newEnv(t)
			repo := newRepo(t, env, "my-app")
			runHook(t, env, "claude-personal", "SessionStart", "abc123", repo)
			runHook(t, env, "claude-personal", "UserPromptSubmit", "abc123", repo)
			runHook(t, env, "claude-personal", tt.event, "abc123", repo)

			rows := lsRows(t, env)

			if len(rows) != 1 || rows[0][4] != tt.status {
				t.Errorf("rows = %v, want one Session with status %s", rows, tt.status)
			}
		})
	}
}

func TestLs_AWholeTurnWalksThroughTheStatuses(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	steps := []struct{ event, status string }{
		{"SessionStart", "waiting_input"},
		{"UserPromptSubmit", "running"},
		{"PreToolUse", "running"},
		{"PermissionRequest", "waiting_approval"},
		{"PostToolUse", "running"},
		{"Stop", "waiting_input"},
		{"UserPromptSubmit", "running"},
		{"Stop", "waiting_input"},
		{"SessionEnd", "ended"},
	}
	for _, step := range steps {
		runHook(t, env, "claude-personal", step.event, "abc123", repo)
		if rows := lsRows(t, env); len(rows) != 1 || rows[0][4] != step.status {
			t.Fatalf("after %s: rows = %v, want status %s", step.event, rows, step.status)
		}
	}
}

func TestLs_AnEventTheStatusTableDoesNotKnowKeepsTheEarlierStatus(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	runHook(t, env, "claude-personal", "UserPromptSubmit", "abc123", repo)
	in := `{"session_id": "abc123", "cwd": "` + repo + `", "hook_event_name": "Notification", "message": "hello"}`
	env.RunWithStdin(in, hookArgs("claude-personal")...)

	rows := lsRows(t, env)

	if len(rows) != 1 || rows[0][4] != "running" {
		t.Errorf("rows = %v, want running", rows)
	}
}

func TestLs_ASessionWithOnlyUnknownEventsIsListedAsUnknown(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	in := `{"session_id": "abc123", "cwd": "` + repo + `", "hook_event_name": "Notification"}`
	env.RunWithStdin(in, hookArgs("claude-personal")...)

	rows := lsRows(t, env)

	if len(rows) != 1 || rows[0][4] != "unknown" {
		t.Errorf("rows = %v, want unknown", rows)
	}
}

func TestLs_ListsEverySessionAcrossProjectsAndProfilesMostRecentFirst(t *testing.T) {
	env := newEnv(t)
	app := newRepo(t, env, "app")
	api := newRepo(t, env, "api")
	runHook(t, env, "claude-personal", "Stop", "s1", app)
	runHook(t, env, "claude-work", "UserPromptSubmit", "s2", api)
	runHook(t, env, "claude-personal", "PermissionRequest", "s3", app)

	rows := lsRows(t, env)

	want := [][]string{
		{"app", "claude", "claude-personal", "host", "waiting_approval"},
		{"api", "claude", "claude-work", "host", "running"},
		{"app", "claude", "claude-personal", "host", "waiting_input"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows = %v, want %v", rows, want)
	}
}

func TestLs_ASessionOutsideGitHasNoProject(t *testing.T) {
	env := newEnv(t)
	dir := env.Path("notes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runHook(t, env, "claude-personal", "Stop", "abc123", dir)
	runHook(t, env, "claude-personal", "Stop", "def456", env.Path("gone")) // does not exist

	rows := lsRows(t, env)

	for _, row := range rows {
		if row[0] != "-" {
			t.Errorf("row = %v, want Project -", row)
		}
	}
	if len(rows) != 2 {
		t.Errorf("rows = %v, want 2", rows)
	}
	if env.Exists(".baton/projects") {
		t.Error("a Project was registered for a Session outside git")
	}
}

func TestLs_AProjectIsKnownByTheDirectoryOfTheFirstEventNotByWhereTheSessionWentLater(t *testing.T) {
	env := newEnv(t)
	app := newRepo(t, env, "app")
	other := newRepo(t, env, "other")
	runHook(t, env, "claude-personal", "SessionStart", "abc123", app)
	runHook(t, env, "claude-personal", "Stop", "abc123", other)

	rows := lsRows(t, env)

	if len(rows) != 1 || rows[0][0] != "app" {
		t.Errorf("rows = %v, want the Session in Project app", rows)
	}
}

func TestLs_ASessionInASubdirectoryBelongsToTheProjectOfTheGitRoot(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	sub := filepath.Join(repo, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	runHook(t, env, "claude-personal", "Stop", "abc123", sub)

	rows := lsRows(t, env)

	if len(rows) != 1 || rows[0][0] != "my-app" {
		t.Errorf("rows = %v", rows)
	}
	if got := env.ReadFile(".baton/projects/my-app/project.toml"); !strings.Contains(got, `path = "`+repo+`"`) {
		t.Errorf("project.toml = %q, want the git root %s", got, repo)
	}
}

func TestLs_RegistersAnUnknownProjectWithTheHostRuntimeUnlessItHasASandbox(t *testing.T) {
	env := newEnv(t)
	plain := newRepo(t, env, "plain")
	boxed := newRepo(t, env, "boxed")
	env.WriteFile(filepath.Join(boxed, ".ai-sandbox", "run.sh"), "#!/bin/sh\n", true)
	runHook(t, env, "claude-personal", "Stop", "s1", plain)
	runHook(t, env, "claude-personal", "Stop", "s2", boxed)

	lsRows(t, env)

	for project, want := range map[string]string{
		"plain": "path = \"" + plain + "\"\nruntime = \"host\"\n",
		"boxed": "path = \"" + boxed + "\"\nruntime = \"sandbox\"\n",
	} {
		if got := env.ReadFile(".baton/projects/" + project + "/project.toml"); got != want {
			t.Errorf("%s: project.toml = %q, want %q", project, got, want)
		}
	}
	// The Runtime of the Session is the one it reported, not the default of its Project.
	for _, row := range lsRows(t, env) {
		if row[3] != "host" {
			t.Errorf("row = %v, want Runtime host as reported by the hook", row)
		}
	}
}

func TestLs_AProjectNameThatIsTakenGetsASuffixAndEachRepositoryKeepsItsName(t *testing.T) {
	env := newEnv(t)
	first := newRepo(t, env, "app")
	second := env.Path("elsewhere", "app")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	if res := env.Command("git", "-C", second, "init", "--quiet"); res.ExitCode != 0 {
		t.Fatal(res.Stderr)
	}
	runHook(t, env, "claude-personal", "Stop", "s1", first)
	lsRows(t, env)
	runHook(t, env, "claude-personal", "UserPromptSubmit", "s2", second)

	rows := lsRows(t, env)
	again := lsRows(t, env)

	want := [][]string{
		{"app-2", "claude", "claude-personal", "host", "running"},
		{"app", "claude", "claude-personal", "host", "waiting_input"},
	}
	if !reflect.DeepEqual(rows, want) || !reflect.DeepEqual(again, want) {
		t.Errorf("rows = %v, again = %v, want %v", rows, again, want)
	}
}

func TestLs_ADirectoryNameThatIsNotAValidProjectNameIsCleanedUp(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "My Weird_App (copy)")
	runHook(t, env, "claude-personal", "Stop", "abc123", repo)

	rows := lsRows(t, env)

	if len(rows) != 1 || rows[0][0] != "my-weird_app-copy" {
		t.Errorf("rows = %v", rows)
	}
}

func TestLs_ParallelReadersRegisterEachNewProjectOnce(t *testing.T) {
	env := newEnv(t)
	var repos []string
	for i := 0; i < 4; i++ {
		repo := env.Path("batch", fmt.Sprint(i), "app") // four repositories called app
		if err := os.MkdirAll(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		if res := env.Command("git", "-C", repo, "init", "--quiet"); res.ExitCode != 0 {
			t.Fatal(res.Stderr)
		}
		runHook(t, env, "claude-personal", "Stop", fmt.Sprint("s", i), repo)
		repos = append(repos, repo)
	}

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if res := env.Run("ls"); res.ExitCode != 0 {
				t.Errorf("ls: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
			}
		}()
	}
	wg.Wait()

	if got := env.ListDir(".baton/projects"); !reflect.DeepEqual(got, []string{".lock", "app", "app-2", "app-3", "app-4"}) {
		t.Errorf("projects = %v, want app, app-2, app-3 and app-4", got)
	}
	paths := make(map[string]bool)
	for _, name := range []string{"app", "app-2", "app-3", "app-4"} {
		paths[env.ReadFile(".baton/projects/"+name+"/project.toml")] = true
	}
	if len(paths) != len(repos) {
		t.Errorf("%d different records for %d repositories", len(paths), len(repos))
	}
}

func TestLs_ReadsTheLastEventOfAFileWhoseLinesAreLargerThanAReadChunk(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	big := strings.Repeat("x", 200_000)
	for i := 0; i < 5; i++ {
		in := `{"session_id": "abc123", "cwd": "` + repo + `", "hook_event_name": "PostToolUse", "tool_response": {"stdout": "` + big + `"}}`
		env.RunWithStdin(in, hookArgs("claude-personal")...)
	}
	runHook(t, env, "claude-personal", "PermissionRequest", "abc123", repo)

	rows := lsRows(t, env)

	if len(rows) != 1 || rows[0][4] != "waiting_approval" {
		t.Errorf("rows = %v, want waiting_approval", rows)
	}
}

func TestLs_IgnoresALineThatIsStillBeingWrittenAndOneThatIsNotAnEvent(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	runHook(t, env, "claude-personal", "UserPromptSubmit", "abc123", repo)
	file := env.Path(".baton/sessions/claude-abc123.jsonl")
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	// A line that is not an Event, then half of one (no newline yet).
	if _, err := f.WriteString("not json\n{\"v\":1,\"ts\":\"2026-10-20T10:00:00Z\",\"agent\":\"claude\",\"event\":\"Stop"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	rows := lsRows(t, env)

	if len(rows) != 1 || rows[0][4] != "running" {
		t.Errorf("rows = %v, want running", rows)
	}
}

func TestLs_IgnoresFilesThatAreNotEventFiles(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	runHook(t, env, "claude-personal", "Stop", "abc123", repo)
	env.WriteFile(".baton/sessions/notes.txt", "hello\n")
	env.WriteFile(".baton/sessions/claude-empty.jsonl", "")

	rows := lsRows(t, env)

	if len(rows) != 1 {
		t.Errorf("rows = %v, want one Session", rows)
	}
}

// TestLs_ASessionStartedByHandShowsUpAfterSync follows the whole path of the
// ticket: sync writes the hooks, "Claude" (the claude stub) runs the command
// of a hook through sh -c like Claude Code does, and ls shows the Session.
func TestLs_ASessionStartedByHandShowsUpAfterSync(t *testing.T) {
	env := newEnv(t)
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	// sync writes the command for ~/.baton/bin/baton; put the binary there.
	binary, err := os.ReadFile(batonBin)
	if err != nil {
		t.Fatal(err)
	}
	env.WriteFile(".baton/bin/baton", string(binary), true)
	repo := newRepo(t, env, "my-app")

	// Claude runs the Stop hook of the Profile's settings.json.
	command := hookCommands(t, env, filepath.Join(profile, "settings.json"))["Stop"][0]
	env.WriteFile("payload.json", payload(t, "Stop", "abc123", repo))
	env.Stub("claude", batontest.WithScript(`sh -c '`+strings.ReplaceAll(command, `'`, `'\''`)+`' < "`+env.Path("payload.json")+`"`))
	if res := env.Command("claude"); res.ExitCode != 0 {
		t.Fatalf("claude: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	rows := lsRows(t, env)

	want := [][]string{{"my-app", "claude", "claude-personal", "host", "waiting_input"}}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows = %v, want %v", rows, want)
	}
}
