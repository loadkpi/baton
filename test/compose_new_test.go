package test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/loadkpi/baton/internal/batontest"
)

// batonMountedContainer adds the fake container of service "app" the way a
// user's override file makes it: the Project's checkout at /workspace and the
// two Baton mounts. It also puts the Baton binary where `make install` does,
// because Baton checks it exists before it lets a container mount it.
func batonMountedContainer(env *batontest.Env, repo string, running bool) *batontest.Container {
	env.WriteFile(".baton/bin/baton", "#!/bin/sh\n", true)
	return env.AddContainer(batontest.ContainerSpec{
		Service: "app", Running: running, User: "1001:1001", Home: "/home/dev",
		Binds:   map[string]string{repo: "/workspace", env.Path(".baton/projects/app"): "/baton/project"},
		BindsRO: map[string]string{env.Path(".baton/bin"): "/baton/bin"},
	})
}

// execLine is the line `baton new` types into the Guake tab for a Claude
// Session of Project "app" at repo, started in /workspace of the container.
func execLine(repo string) string {
	return "cd " + repo + " && docker compose -f " + filepath.Join(repo, "docker-compose.yml") +
		" exec -w /workspace app env -u ANTHROPIC_API_KEY -u OPENAI_API_KEY -u CLAUDE_CONFIG_DIR claude"
}

func TestComposeNew_OpensATabThatExecsClaudeInTheProjectsDirectoryOfTheRunningContainer(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(composeConfig(env, ""))
	repo := composeProject(t, env, "app")
	batonMountedContainer(env, repo, true)

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	got := guakeArgs(env)
	for _, call := range got {
		call[3] = stable(call[3])
	}
	want := [][]string{{"-e", execLine(repo), "-r", "app/YYYY-MM-DD-1:claude"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("guake calls = %q, want %q", got, want)
	}
	for _, call := range composeDockerCalls(env) {
		if startsOrChangesAService(call) {
			t.Errorf("docker call %q starts or changes a service that was running", call)
		}
	}
}

func TestComposeNew_AStoppedServiceIsBroughtUpWithoutBeingRecreated(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(composeConfig(env, ""))
	repo := composeProject(t, env, "app")
	batonMountedContainer(env, repo, false)

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	up := "compose -f " + filepath.Join(repo, "docker-compose.yml") + " up -d app"
	calls := composeDockerCalls(env)
	if !slices.Contains(calls, up) {
		t.Errorf("docker calls = %q, want %q among them", calls, up)
	}
	for _, call := range calls {
		if strings.Contains(call, "recreate") || strings.Contains(call, " down") || strings.Contains(call, " stop") || strings.Contains(call, " rm ") {
			t.Errorf("docker call %q recreates or removes the container", call)
		}
	}
	if len(guakeArgs(env)) != 1 {
		t.Errorf("guake calls = %q, want one tab", guakeArgs(env))
	}
}

// composeHook plays an Agent in the container calling the hook Baton's sync put
// in the Compose Profile of Project app, for one Event of a Claude Session.
func composeHook(t *testing.T, env *batontest.Env, root, event, sessionID, cwd string) {
	t.Helper()
	res := env.RunWithEnv([]string{"BATON_CONTAINER_ROOT=" + root}, payload(t, event, sessionID, cwd),
		"hook", "claude", "--profile", "app.claude", "--runtime", "compose")
	if res.ExitCode != 0 || res.Stdout != "" || res.Stderr != "" {
		t.Fatalf("hook %s: exit code = %d, stdout=%q, stderr=%q, want a silent success", event, res.ExitCode, res.Stdout, res.Stderr)
	}
}

// startedByHand is the setup of a user who runs Claude in the container
// themselves: the Project, a running container with Baton's mounts and the
// root of the hook's view of it.
func startedByHand(t *testing.T, env *batontest.Env) (repo, root string) {
	t.Helper()
	env.WriteConfig(composeConfig(env, ""))
	repo = composeProject(t, env, "app")
	batonMountedContainer(env, repo, true)
	return repo, containerRoot(t, env, "app")
}

func TestComposeNew_AMissingMountIsRefusedWithTheSnippetForTheOverrideFileAndNothingIsChanged(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(composeConfig(env, ""))
	repo := composeProject(t, env, "app")
	env.WriteFile(".baton/bin/baton", "#!/bin/sh\n", true)
	// The service mounts the checkout and nothing of Baton's.
	env.AddContainer(batontest.ContainerSpec{Service: "app", Running: true, Binds: map[string]string{repo: "/workspace"}})
	before := env.ReadFile(filepath.Join(repo, "docker-compose.yml"))

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want a refusal; stdout=%q", res.Stdout)
	}
	snippet := "services:\n" +
		"  app:\n" +
		"    volumes:\n" +
		"      - " + env.Path(".baton/bin") + ":/baton/bin:ro\n" +
		"      - " + env.Path(".baton/projects/app") + ":/baton/project:rw\n"
	for _, want := range []string{
		"/baton/bin is not mounted", "/baton/project is not mounted",
		snippet,
		"needs one recreate of the container",
		"ends every Session running in it",
	} {
		if !strings.Contains(res.Stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, want)
		}
	}
	if len(env.Calls("guake")) != 0 {
		t.Error("a tab was opened for a container without the mounts")
	}
	for _, call := range composeDockerCalls(env) {
		if startsOrChangesAService(call) || strings.Contains(call, " exec ") {
			t.Errorf("docker call %q changes the service or runs something in it", call)
		}
	}
	if got := env.ReadFile(filepath.Join(repo, "docker-compose.yml")); got != before {
		t.Errorf("docker-compose.yml = %q, want it unchanged", got)
	}
	if got := env.ListDir(repo); !slices.Equal(got, []string{".git", "docker-compose.yml"}) {
		t.Errorf("the Project holds %v, want no new file", got)
	}
}

func TestComposeNew_OnlyTheMountsThatAreWrongAreInTheSnippet(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(composeConfig(env, ""))
	repo := composeProject(t, env, "app")
	env.WriteFile(".baton/bin/baton", "#!/bin/sh\n", true)
	// bin is writable from the container, and the Project's directory is from
	// somewhere else.
	env.AddContainer(batontest.ContainerSpec{Service: "app", Running: true, Binds: map[string]string{
		repo: "/workspace", env.Path(".baton/bin"): "/baton/bin", env.Path("elsewhere"): "/baton/project",
	}})

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want a refusal")
	}
	for _, want := range []string{
		"/baton/bin is mounted read-write, not read-only",
		"/baton/project is mounted from " + env.Path("elsewhere") + ", not from " + env.Path(".baton/projects/app"),
		"      - " + env.Path(".baton/bin") + ":/baton/bin:ro\n",
		"      - " + env.Path(".baton/projects/app") + ":/baton/project:rw\n",
	} {
		if !strings.Contains(res.Stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, want)
		}
	}
}

func TestComposeNew_AStoppedServiceWithoutTheMountsIsBroughtUpAndRefusedNeverRecreated(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(composeConfig(env, ""))
	repo := composeProject(t, env, "app")
	env.WriteFile(".baton/bin/baton", "#!/bin/sh\n", true)
	env.AddContainer(batontest.ContainerSpec{Service: "app", Running: false, Binds: map[string]string{repo: "/workspace"}})

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "/baton/bin is not mounted") {
		t.Errorf("exit code = %d, stderr=%q, want the missing mounts reported", res.ExitCode, res.Stderr)
	}
	for _, call := range composeDockerCalls(env) {
		if strings.Contains(call, "recreate") {
			t.Errorf("docker call %q recreates the container", call)
		}
	}
	if len(env.Calls("guake")) != 0 {
		t.Error("a tab was opened")
	}
}

func TestComposeNew_WithoutTheBatonBinaryToMountIsRefusedWithAHintBeforeDockerIsAsked(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(composeConfig(env, ""))
	repo := composeProject(t, env, "app")
	batonMountedContainer(env, repo, true)
	if err := os.Remove(env.Path(".baton/bin/baton")); err != nil {
		t.Fatal(err)
	}

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "make install") {
		t.Errorf("exit code = %d, stderr=%q, want a refusal that says to run make install", res.ExitCode, res.Stderr)
	}
	if len(env.Calls("docker")) != 0 || len(env.Calls("guake")) != 0 {
		t.Errorf("docker calls = %v, guake calls = %v, want none", env.Calls("docker"), env.Calls("guake"))
	}
}

func TestComposeNew_AServiceThatDoesNotComeUpOpensNoTab(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(composeConfig(env, ""))
	composeProject(t, env, "app")
	env.WriteFile(".baton/bin/baton", "#!/bin/sh\n", true)
	// The default docker stub knows no container: `up -d` "succeeds" and
	// nothing runs afterwards.

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "not running after docker compose up -d") {
		t.Errorf("exit code = %d, stderr=%q, want it to say the service is not running", res.ExitCode, res.Stderr)
	}
	if len(env.Calls("guake")) != 0 {
		t.Error("a tab was opened")
	}
}

func TestComposeNew_CodexIsRefusedUntilItRunsInAComposeContainer(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(composeConfig(env, ""))
	repo := composeProject(t, env, "app")
	batonMountedContainer(env, repo, false)

	res := env.Run("new", "app", "--agent", "codex")

	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "codex does not run in a compose container yet") {
		t.Errorf("exit code = %d, stderr=%q, want codex refused for compose", res.ExitCode, res.Stderr)
	}
	if len(env.Calls("guake")) != 0 || len(env.Calls("docker")) != 0 {
		t.Errorf("guake calls = %v, docker calls = %v, want none", env.Calls("guake"), env.Calls("docker"))
	}
}

func TestComposeNew_AGitWorktreeIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(composeConfig(env, ""))
	repo := composeProject(t, env, "app")
	commitFile(t, env, repo, "README.md", "hello\n", "first commit")
	worktree := addWorktree(t, env, repo, "feature")
	batonMountedContainer(env, repo, true)

	res := env.Run("new", worktree, "--agent", "claude")

	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "runs in the compose Runtime, whose Sessions run only in the main checkout "+repo) {
		t.Errorf("exit code = %d, stderr=%q, want the worktree refused", res.ExitCode, res.Stderr)
	}
	if env.Exists(".baton/projects/app/tasks") || len(env.Calls("guake")) != 0 || len(env.Calls("docker")) != 0 {
		t.Error("something was written or started for a worktree")
	}
}

func TestComposeNew_TheTypedLineRunsClaudeInTheContainerWithoutTheContainersAPIKeys(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(composeConfig(env, ""))
	repo := composeProject(t, env, "app")
	env.WriteFile(".baton/bin/baton", "#!/bin/sh\n", true)
	c := env.AddContainer(batontest.ContainerSpec{
		Service: "app", Running: true, Home: "/home/dev",
		Binds:   map[string]string{repo: "/workspace", env.Path(".baton/projects/app"): "/baton/project"},
		BindsRO: map[string]string{env.Path(".baton/bin"): "/baton/bin"},
		// What the container was created with: the keys, a stale config
		// directory and something the Agent should keep.
		Env: []string{"ANTHROPIC_API_KEY=sk-ant-x", "OPENAI_API_KEY=sk-oai-x", "CLAUDE_CONFIG_DIR=/stale/dir", "KEEP_ME=1"},
	})
	c.MkdirAll("/workspace")
	out := env.Path("claude-env.txt")
	env.Stub("claude", batontest.WithScript(`env | sort > `+out+`; pwd >> `+out))
	if res := env.Run("new", "app", "--agent", "claude"); res.ExitCode != 0 {
		t.Fatalf("new: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	// What Guake does: its shell runs the line, with keys of its own around.
	res := env.Command("sh", "-c", "export ANTHROPIC_API_KEY=host-key; "+typedLine(t, env))

	if res.ExitCode != 0 {
		t.Fatalf("the line failed: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	seen := env.ReadFile("claude-env.txt")
	for _, gone := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "CLAUDE_CONFIG_DIR"} {
		if strings.Contains(seen, gone) {
			t.Errorf("%s is in the Agent's environment:\n%s", gone, seen)
		}
	}
	if !strings.Contains(seen, "KEEP_ME=1\n") {
		t.Errorf("a variable of the container was lost:\n%s", seen)
	}
	if !strings.HasSuffix(seen, "/workspace\n") {
		t.Errorf("the Agent's working directory is not /workspace of the container:\n%s", seen)
	}
}

func TestComposeNew_AProfileOutsideTheDefaultDirectoryIsNamedToClaudeAndSeveralNeedAChoice(t *testing.T) {
	env := newEnv(t)
	repo := composeProject(t, env, "app")
	batonMountedContainer(env, repo, true)
	env.WriteConfig(standardConfig(env) + `
[projects.app.compose]
files   = ["docker-compose.yml"]
service = "app"

[projects.app.compose.profiles.main]
agent = "claude"
dir   = "~/.claude"

[projects.app.compose.profiles.volume]
agent = "claude"
dir   = "/state/.claude"
`)

	ambiguous := env.Run("new", "app", "--agent", "claude")
	if ambiguous.ExitCode == 0 || !strings.Contains(ambiguous.Stderr, "several claude profiles in the compose service of project app (app.main, app.volume); choose one with --profile") {
		t.Errorf("exit code = %d, stderr=%q, want the choice demanded", ambiguous.ExitCode, ambiguous.Stderr)
	}
	chosen := env.Run("new", "app", "--agent", "claude", "--profile", "app.volume")
	if chosen.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", chosen.ExitCode, chosen.Stderr)
	}
	if got, want := typedLine(t, env), strings.Replace(execLine(repo), "-u CLAUDE_CONFIG_DIR", "CLAUDE_CONFIG_DIR=/state/.claude", 1); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
	unknown := env.Run("new", "app", "--agent", "claude", "--profile", "claude-personal")
	if unknown.ExitCode == 0 || !strings.Contains(unknown.Stderr, `unknown profile "claude-personal"`) {
		t.Errorf("exit code = %d, stderr=%q, want a host Profile refused", unknown.ExitCode, unknown.Stderr)
	}
}

func TestComposeLs_ASessionStartedByHandShowsUpInItsProjectWorkingCopyAndTask(t *testing.T) {
	env := newEnv(t)
	repo, root := startedByHand(t, env)
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}

	// The container sees the Project at /workspace, which is not where it is
	// on the host; nobody ran `baton new`.
	composeHook(t, env, root, "SessionStart", "s1", "/workspace/pkg")
	waiting := lsRows(t, env)
	composeHook(t, env, root, "PreToolUse", "s1", "/workspace/pkg")
	running := lsRows(t, env)

	want := func(status string) [][]string {
		return [][]string{{"app", "YYYY-MM-DD-1", "claude", "s1", "app.claude", "compose", status}}
	}
	if !reflect.DeepEqual(waiting, want("waiting_input")) || !reflect.DeepEqual(running, want("running")) {
		t.Errorf("rows = %v and %v, want %v and %v", waiting, running, want("waiting_input"), want("running"))
	}
	if got := env.ListDir(".baton/projects"); !reflect.DeepEqual(got, []string{"app"}) {
		t.Errorf("projects = %v, want only app, not a Project at the container's path", got)
	}
}

func TestComposeLs_ASessionOfAnotherAgentIsShownToo(t *testing.T) {
	env := newEnv(t)
	_, root := startedByHand(t, env)

	res := env.RunWithEnv([]string{"BATON_CONTAINER_ROOT=" + root}, fixture(t, "codex", "Stop", "thr_9", "/workspace"),
		"hook", "codex", "--profile", "app.codex", "--runtime", "compose")
	if res.ExitCode != 0 {
		t.Fatalf("hook: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	want := [][]string{{"app", "YYYY-MM-DD-1", "codex", "thr_9", "app.codex", "compose", "waiting_input"}}
	if got := lsRows(t, env); !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
}

func TestComposeLs_SaysThatLostIsNotCheckedForComposeYetAndKeepsTheEventStatus(t *testing.T) {
	env := newEnv(t)
	_, root := startedByHand(t, env)
	composeHook(t, env, root, "UserPromptSubmit", "s1", "/workspace")

	res := env.Run("ls")

	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "running") {
		t.Errorf("exit code = %d, stdout=%q, want the status the Events give", res.ExitCode, res.Stdout)
	}
	if want := "baton: warning: cannot tell whether compose sessions are still running, showing what their events say: compose sessions are not checked for being lost yet\n"; res.Stderr != want {
		t.Errorf("stderr = %q, want %q", res.Stderr, want)
	}
}

func TestComposeLs_TheLongestMatchingMountMapsAPath(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(composeConfig(env, ""))
	repo := composeProject(t, env, "app")
	lib := newRepo(t, env, "lib")
	if err := os.MkdirAll(filepath.Join(repo, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	env.WriteFile(".baton/bin/baton", "#!/bin/sh\n", true)
	env.AddContainer(batontest.ContainerSpec{
		Service: "app", Running: true,
		// A second checkout is mounted inside the Project's.
		Binds:   map[string]string{repo: "/workspace", lib: "/workspace/vendor/lib", env.Path(".baton/projects/app"): "/baton/project"},
		BindsRO: map[string]string{env.Path(".baton/bin"): "/baton/bin"},
	})
	root := containerRoot(t, env, "app")

	composeHook(t, env, root, "Stop", "s1", "/workspace/vendor/lib")
	composeHook(t, env, root, "Stop", "s2", "/workspace/vendor")

	var projects []string
	for _, r := range lsRows(t, env) {
		projects = append(projects, r[0])
	}
	slices.Sort(projects)
	if !slices.Equal(projects, []string{"app", "lib"}) {
		t.Errorf("projects of the Sessions = %v, want lib (under the inner mount) and app", projects)
	}
}

func TestComposeSession_BlocksAHostSessionInTheSameMainCheckoutUntilItEnds(t *testing.T) {
	env := newEnv(t)
	repo, root := startedByHand(t, env)
	composeHook(t, env, root, "SessionStart", "s1", "/workspace")

	res := env.Run("new", repo, "--agent", "claude")
	byName := env.Run("new", "app", "--agent", "claude")

	for _, r := range []batontest.Result{res, byName} {
		if r.ExitCode != 1 || !strings.Contains(r.Stderr, "already has an active session: claude, profile app.claude, session s1") {
			t.Errorf("exit code = %d, stderr=%q, want the active compose Session named", r.ExitCode, r.Stderr)
		}
	}
	if len(env.Calls("guake")) != 0 {
		t.Error("a tab was opened while the Session was active")
	}
	composeHook(t, env, root, "SessionEnd", "s1", "/workspace")
	if res := env.Run("new", "app", "--agent", "claude"); res.ExitCode != 0 {
		t.Errorf("after SessionEnd: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
}

func TestHostSession_BlocksAComposeSessionInTheSameMainCheckout(t *testing.T) {
	env := newEnv(t)
	repo, _ := startedByHand(t, env)
	hostEvent(t, env, "h1", "2031-03-04T10:00:00Z", "SessionStart", repo, proc{})

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "already has an active session: claude, profile claude-personal, session h1") {
		t.Errorf("exit code = %d, stderr=%q, want the active host Session named", res.ExitCode, res.Stderr)
	}
	if len(env.Calls("guake")) != 0 {
		t.Error("a tab was opened while a host Session was active")
	}
}

func TestComposeHandoff_ContinuesInTheContainerAndNamesTheHandoffFileAsTheContainerSeesIt(t *testing.T) {
	env := newEnv(t)
	repo, root := startedByHand(t, env)
	commitFile(t, env, repo, "README.md", "hello\n", "first commit")
	composeHook(t, env, root, "SessionStart", "s1", "/workspace")
	composeHook(t, env, root, "SessionEnd", "s1", "/workspace")

	res := env.Run("handoff", "app/"+firstTask(t, env), "--to", "claude")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	line := typedLine(t, env)
	if !strings.Contains(line, " exec -w /workspace app env ") {
		t.Errorf("line = %q, want a compose exec in the container", line)
	}
	if !strings.Contains(line, "continue the Task from Handoff /baton/project/handoffs/") {
		t.Errorf("line = %q, want the Handoff named by its path in the container", line)
	}
}

// firstTask is the id of the only Task `baton ls` shows.
func firstTask(t *testing.T, env *batontest.Env) string {
	t.Helper()
	rows := lsRowsRaw(t, env)
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want one", rows)
	}
	return rows[0][1]
}
