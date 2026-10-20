package test

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/loadkpi/baton/internal/batontest"
)

// imageTag is the image of the copy of ai-sandbox in testdata/ai-sandbox: the
// tag run.sh builds from its versions.env.
const imageTag = "ai-sandbox:codex-0.162.1_claude-2.1.296"

// launchProject is a Project "app" that runs in the sandbox with a real copy of
// ai-sandbox's run.sh, and a Baton binary in ~/.baton/bin for the container to
// mount. It returns the main checkout and the sandbox's home directory.
func launchProject(t *testing.T, env *batontest.Env) (repo, home string) {
	t.Helper()
	repo, home = sandboxProject(t, env, "app")
	installAiSandbox(t, env, repo)
	env.WriteFile(".baton/bin/baton", "#!/bin/sh\n", true)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	return repo, home
}

// dockerCalls returns the arguments of every recorded docker call whose first
// argument is verb.
func dockerCalls(env *batontest.Env, verb string) [][]string {
	var out [][]string
	for _, c := range env.Calls("docker") {
		if len(c.Args) > 0 && c.Args[0] == verb {
			out = append(out, c.Args)
		}
	}
	return out
}

// runTabLine types the line of the only Guake tab into a shell, the way the
// tab's own shell reads it, and returns the arguments the docker stub got for
// `docker run`.
func runTabLine(t *testing.T, env *batontest.Env) []string {
	t.Helper()
	line := typedLine(t, env)
	res := env.Command("sh", "-c", line)
	if res.ExitCode != 0 {
		t.Fatalf("the tab line %q failed: exit code = %d, stderr=%q", line, res.ExitCode, res.Stderr)
	}
	runs := dockerCalls(env, "run")
	if len(runs) != 1 {
		t.Fatalf("docker run calls = %q, want exactly one", runs)
	}
	return runs[0]
}

// volumes returns the values of the -v options of a docker invocation.
func volumes(args []string) []string {
	var out []string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-v" {
			out = append(out, args[i+1])
		}
	}
	return out
}

func TestSandboxNew_OpensATabThatRunsDockerWithExactlyTwoBatonMounts(t *testing.T) {
	env := newEnv(t)
	repo, home := launchProject(t, env)

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if line := typedLine(t, env); !strings.HasPrefix(line, "cd "+repo+" && docker run ") {
		t.Errorf("tab line = %q, want it to change into the Project and run docker", line)
	}
	if title := stable(guakeArgs(env)[0][3]); title != "app/YYYY-MM-DD-1:claude" {
		t.Errorf("tab title = %q, want app/YYYY-MM-DD-1:claude", title)
	}

	args := runTabLine(t, env)

	var baton, own []string
	for _, v := range volumes(args) {
		if strings.Contains(v, ":/baton/") {
			baton = append(baton, v)
		} else {
			own = append(own, v)
		}
	}
	wantBaton := []string{
		env.Path(".baton", "bin") + ":/baton/bin:ro",
		env.Path(".baton", "projects", "app") + ":/baton/project:rw",
	}
	if strings.Join(baton, "\n") != strings.Join(wantBaton, "\n") {
		t.Errorf("Baton mounts = %q, want %q", baton, wantBaton)
	}
	if len(own) < 2 || own[0] != repo+":/workspace:rw" || own[1] != home+":/home/sandbox:rw" {
		t.Errorf("ai-sandbox mounts = %q, want its own mounts (workspace, home, ...) left as run.sh made them", own)
	}
	wantTail := []string{imageTag, "env", "-u", "ANTHROPIC_API_KEY", "-u", "OPENAI_API_KEY", "claude"}
	if got := args[len(args)-len(wantTail):]; strings.Join(got, " ") != strings.Join(wantTail, " ") {
		t.Errorf("end of the docker command = %q, want %q", got, wantTail)
	}
	if _, err := os.Stat(filepath.Join(repo, ".ai-sandbox", "home", ".codex", "config.toml")); err != nil {
		t.Errorf("run.sh did not render the Codex config: %v", err)
	}
}

func TestSandboxNew_NoHostAPIKeyReachesTheContainer(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	// run.sh passes a credentials file to docker as it is; the user may have
	// put anything in it.
	creds := env.Path(".config", "ai-sandbox", "credentials.env")
	env.WriteFile(creds, "ANTHROPIC_API_KEY=sk-ant-from-file\nGITHUB_TOKEN=ghp-from-file\n")

	res := env.RunWithEnv([]string{"ANTHROPIC_API_KEY=sk-ant-from-host", "OPENAI_API_KEY=sk-oai-from-host"}, "", "new", "app", "--agent", "claude")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	args := runTabLine(t, env)
	for i, a := range args {
		if strings.Contains(a, "sk-ant-from-host") || strings.Contains(a, "sk-oai-from-host") {
			t.Errorf("docker arg %d = %q carries a key of the host", i, a)
		}
		if (a == "-e" || a == "--env") && i+1 < len(args) && !strings.Contains(args[i+1], "=") {
			t.Errorf("docker passes the variable %q through from the host", args[i+1])
		}
	}
	// What the credentials file holds goes in by file name, and the Agent is
	// started without the keys whatever its source.
	if !contains(args, "--env-file") || !contains(args, creds) {
		t.Errorf("docker args = %q, want run.sh's --env-file %s kept", args, creds)
	}
	want := []string{"env", "-u", "ANTHROPIC_API_KEY", "-u", "OPENAI_API_KEY", "claude"}
	if got := args[len(args)-len(want):]; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("command in the container = %q, want %q", got, want)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// buildScript is a stand-in for ai-sandbox's build.sh that records that it ran.
const buildScript = "#!/bin/sh\necho built >> \"$HOME/build.log\"\n"

func TestSandboxNew_BuildsAMissingImageBeforeOpeningTheTab(t *testing.T) {
	env := newEnv(t)
	repo, _ := launchProject(t, env)
	env.WriteFile(filepath.Join(repo, ".ai-sandbox", "build.sh"), buildScript, true)
	env.Stub("docker", batontest.WithScript(`[ "$1" = image ] && exit 1; exit 0`))

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := env.ReadFile("build.log"); got != "built\n" {
		t.Errorf("build.log = %q, want one build", got)
	}
	inspects := dockerCalls(env, "image")
	if len(inspects) != 1 || strings.Join(inspects[0], " ") != "image inspect "+imageTag {
		t.Errorf("docker image calls = %q, want one `image inspect %s`", inspects, imageTag)
	}
	if len(guakeArgs(env)) != 1 {
		t.Errorf("guake calls = %q, want the tab opened after the build", guakeArgs(env))
	}
}

func TestSandboxNew_DoesNotBuildAnImageThatExists(t *testing.T) {
	env := newEnv(t)
	repo, _ := launchProject(t, env)
	env.WriteFile(filepath.Join(repo, ".ai-sandbox", "build.sh"), buildScript, true)

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if env.Exists("build.log") {
		t.Error("build.sh ran although the image exists")
	}
	if inspects := dockerCalls(env, "image"); len(inspects) != 1 {
		t.Errorf("docker image calls = %q, want one image inspect", inspects)
	}
}

func TestSandboxNew_AFailedBuildOpensNoTab(t *testing.T) {
	env := newEnv(t)
	repo, _ := launchProject(t, env)
	env.WriteFile(filepath.Join(repo, ".ai-sandbox", "build.sh"), "#!/bin/sh\necho 'no space left' >&2\nexit 1\n", true)
	env.Stub("docker", batontest.WithScript(`[ "$1" = image ] && exit 1; exit 0`))

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "build image "+imageTag) || !strings.Contains(res.Stderr, "no space left") {
		t.Errorf("exit code = %d, stderr=%q, want the build failure and its output", res.ExitCode, res.Stderr)
	}
	if calls := env.Calls("guake"); len(calls) != 0 {
		t.Errorf("guake was called: %v", calls)
	}
}

// fragmentSource is a source with instructions and an MCP server, which sync
// turns into the Codex fragments of the sandbox.
func fragmentSource(env *batontest.Env, instructions, command string) {
	env.SourceFile("AGENTS.md", instructions)
	env.SourceFile("mcp.json", `{"mcpServers": {"files": {"command": "`+command+`"}}}`)
	env.CommitSource("content")
}

func syncOK(t *testing.T, env *batontest.Env) {
	t.Helper()
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
}

func newOK(t *testing.T, env *batontest.Env) {
	t.Helper()
	if res := env.Run("new", "app", "--agent", "claude"); res.ExitCode != 0 {
		t.Fatalf("new: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
}

func TestSandboxNew_BatonsFragmentsEndUpBetweenItsOwnMarkersInTheRenderedCodexFiles(t *testing.T) {
	env := newEnv(t)
	fragmentSource(env, "Be brief.\n", "npx")
	_, home := launchProject(t, env)
	syncOK(t, env)
	config, agents := filepath.Join(home, ".codex", "config.toml"), filepath.Join(home, ".codex", "AGENTS.md")

	newOK(t, env)

	gotConfig, gotAgents := env.ReadFile(config), env.ReadFile(agents)
	if !strings.HasPrefix(gotConfig, "# managed-by: ai-sandbox/run.sh") || !strings.Contains(gotConfig, "[shell_environment_policy]") {
		t.Errorf("config.toml = %q, want what run.sh rendered kept", gotConfig)
	}
	if want := "\n# baton:begin\n# Written by baton sync. Do not edit.\n[mcp_servers.files]\ncommand = \"npx\"\n# baton:end\n"; !strings.HasSuffix(gotConfig, want) {
		t.Errorf("config.toml ends with %q, want it to end with %q", gotConfig[max(0, len(gotConfig)-120):], want)
	}
	if !strings.HasPrefix(gotAgents, "<!-- managed-by: ai-sandbox/run.sh") {
		t.Errorf("AGENTS.md = %q, want what run.sh rendered kept", gotAgents)
	}
	if want := "\n<!-- baton:begin -->\nBe brief.\n<!-- baton:end -->\n"; !strings.HasSuffix(gotAgents, want) {
		t.Errorf("AGENTS.md ends with %q, want it to end with %q", gotAgents[max(0, len(gotAgents)-120):], want)
	}
	if mode := env.Mode(config); mode != 0o600 {
		t.Errorf("config.toml mode = %04o, want 0600: the fragment may hold a secret", mode)
	}

	// Launching again renders the files afresh and merges again: one block.
	newOK(t, env)
	if env.ReadFile(config) != gotConfig || env.ReadFile(agents) != gotAgents {
		t.Error("a second launch changed the merged files")
	}
}

func TestSandboxNew_ABlockIsNotDuplicatedWhenTheUserTookAiSandboxsMarkerOut(t *testing.T) {
	env := newEnv(t)
	fragmentSource(env, "Be brief.\n", "npx")
	_, home := launchProject(t, env)
	syncOK(t, env)
	newOK(t, env)
	config, agents := filepath.Join(home, ".codex", "config.toml"), filepath.Join(home, ".codex", "AGENTS.md")
	// The user takes the files over: run.sh now leaves them alone, our block
	// included, and adds a line of their own.
	for _, f := range []string{config, agents} {
		text := env.ReadFile(f)
		text = strings.Replace(text, strings.SplitN(text, "\n", 2)[0], "# mine now", 1) + "\n# mine too\n"
		env.WriteFile(f, text)
	}
	fragmentSource(env, "Be thorough.\n", "uvx")
	syncOK(t, env)

	newOK(t, env)

	gotConfig, gotAgents := env.ReadFile(config), env.ReadFile(agents)
	if n := strings.Count(gotConfig, "# baton:begin"); n != 1 {
		t.Errorf("config.toml has %d Baton blocks, want 1:\n%s", n, gotConfig)
	}
	if n := strings.Count(gotAgents, "<!-- baton:begin -->"); n != 1 {
		t.Errorf("AGENTS.md has %d Baton blocks, want 1:\n%s", n, gotAgents)
	}
	if !strings.Contains(gotConfig, "command = \"uvx\"\n# baton:end\n") || strings.Contains(gotConfig, "npx") {
		t.Errorf("config.toml = %q, want the block updated in place", gotConfig)
	}
	if !strings.Contains(gotAgents, "Be thorough.\n<!-- baton:end -->\n") || strings.Contains(gotAgents, "Be brief.") {
		t.Errorf("AGENTS.md = %q, want the block updated in place", gotAgents)
	}
	for _, text := range []string{gotConfig, gotAgents} {
		if !strings.HasPrefix(text, "# mine now\n") {
			t.Errorf("the user's first line was lost: %q", text)
		}
		if !strings.Contains(text, "# mine too\n") {
			t.Errorf("the user's own line was lost: %q", text)
		}
	}
}

func TestSandboxNew_ABlockWhoseFragmentIsGoneIsRemoved(t *testing.T) {
	env := newEnv(t)
	fragmentSource(env, "Be brief.\n", "npx")
	_, home := launchProject(t, env)
	syncOK(t, env)
	newOK(t, env)
	env.SourceFile("AGENTS.md", "")
	env.Command("git", "-C", env.SourceDir, "rm", "--quiet", "--force", "AGENTS.md", "mcp.json")
	env.CommitSource("remove content")
	syncOK(t, env)

	newOK(t, env)

	for _, rel := range []string{"config.toml", "AGENTS.md"} {
		if text := env.ReadFile(filepath.Join(home, ".codex", rel)); strings.Contains(text, "baton:") {
			t.Errorf("%s still has a Baton block: %q", rel, text)
		}
	}
}

func TestSandboxNew_BrokenMarkersInTheConfigAreReportedAndOpenNoTab(t *testing.T) {
	env := newEnv(t)
	fragmentSource(env, "Be brief.\n", "npx")
	_, home := launchProject(t, env)
	syncOK(t, env)
	config := filepath.Join(home, ".codex", "config.toml")
	env.WriteFile(config, "# mine\n# baton:begin\nunfinished = true\n")

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, config) || !strings.Contains(res.Stderr, "# baton:begin") {
		t.Errorf("exit code = %d, stderr=%q, want an error naming the file and the marker", res.ExitCode, res.Stderr)
	}
	if calls := env.Calls("guake"); len(calls) != 0 {
		t.Errorf("guake was called: %v", calls)
	}
}

// containerRoot makes a directory that stands for the root of a container in
// which the Project's Baton directory is mounted at /baton/project (a symbolic
// link plays the bind mount). The hook is told where the root is by the one
// variable it reads for this, which exists for tests.
func containerRoot(t *testing.T, env *batontest.Env, project string) string {
	t.Helper()
	root := t.TempDir()
	projectDir := env.Path(".baton", "projects", project)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "baton"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(projectDir, filepath.Join(root, "baton", "project")); err != nil {
		t.Fatal(err)
	}
	return root
}

// sandboxHook plays Claude in the container calling the hook Baton's sync put
// in the sandbox Profile of Project app, for one Event.
func sandboxHook(t *testing.T, env *batontest.Env, root, event, sessionID, cwd string) batontest.Result {
	t.Helper()
	return env.RunWithEnv([]string{"BATON_CONTAINER_ROOT=" + root}, payload(t, event, sessionID, cwd),
		"hook", "claude", "--profile", "app.claude", "--runtime", "sandbox")
}

func TestSandboxHook_WritesTheEventIntoTheMountedProjectDirectory(t *testing.T) {
	env := newEnv(t)
	root := containerRoot(t, env, "app")

	res := sandboxHook(t, env, root, "Stop", "abc123", "/workspace")

	if res.ExitCode != 0 || res.Stdout != "" || res.Stderr != "" {
		t.Fatalf("exit code = %d, stdout=%q, stderr=%q, want a silent success", res.ExitCode, res.Stdout, res.Stderr)
	}
	lines := eventLines(t, env, ".baton/projects/app/sessions/claude-abc123.jsonl")
	if len(lines) != 1 || lines[0]["runtime"] != "sandbox" || lines[0]["profile"] != "app.claude" ||
		lines[0]["event"] != "Stop" || lines[0]["cwd"] != "/workspace" || lines[0]["session_id"] != "abc123" {
		t.Errorf("lines = %v, want one sandbox Stop Event of app.claude in /workspace", lines)
	}
	if mode := env.Mode(".baton/projects/app/sessions/claude-abc123.jsonl"); mode != 0o600 {
		t.Errorf("Event file mode = %04o, want 0600", mode)
	}
	if env.Exists(".baton/sessions") {
		t.Error("the Event went to the host's sessions directory")
	}
}

func TestSandboxHook_WithoutTheBatonMountDoesNothingAndSucceeds(t *testing.T) {
	env := newEnv(t)
	root := t.TempDir() // a container that never heard of Baton

	res := sandboxHook(t, env, root, "Stop", "abc123", "/workspace")

	if res.ExitCode != 0 || res.Stdout != "" || res.Stderr != "" {
		t.Errorf("exit code = %d, stdout=%q, stderr=%q, want a silent success", res.ExitCode, res.Stdout, res.Stderr)
	}
	if env.Exists(".baton") {
		t.Error("the hook wrote under ~/.baton")
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("the hook wrote into the container root: %v", entries)
	}
}

func TestSandboxHook_ParallelHooksShareTheEventFileWithoutTearingLines(t *testing.T) {
	env := newEnv(t)
	root := containerRoot(t, env, "app")
	const hooks = 12
	var wg sync.WaitGroup
	for i := 0; i < hooks; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			filler := strings.Repeat(string(rune('a'+i)), 200_000)
			in := `{"session_id": "abc123", "hook_event_name": "PostToolUse", "cwd": "/workspace", "tool_response": {"stdout": "` + filler + `"}}`
			res := env.RunWithEnv([]string{"BATON_CONTAINER_ROOT=" + root}, in, "hook", "claude", "--profile", "app.claude", "--runtime", "sandbox")
			if res.ExitCode != 0 {
				t.Errorf("hook %d: exit code = %d, stderr=%q", i, res.ExitCode, res.Stderr)
			}
		}()
	}
	wg.Wait()

	seen := make(map[string]bool)
	for _, line := range eventLines(t, env, ".baton/projects/app/sessions/claude-abc123.jsonl") {
		stdout := line["payload"].(map[string]any)["tool_response"].(map[string]any)["stdout"].(string)
		if len(stdout) != 200_000 || strings.Trim(stdout, stdout[:1]) != "" {
			t.Fatalf("a line is torn: %d bytes starting with %q", len(stdout), stdout[:1])
		}
		seen[stdout[:1]] = true
	}
	if len(seen) != hooks {
		t.Errorf("%d different lines, want %d", len(seen), hooks)
	}
}

func TestSandboxHook_TheContainerRootMeansNothingToAHostHook(t *testing.T) {
	env := newEnv(t)
	root := containerRoot(t, env, "app")

	res := env.RunWithEnv([]string{"BATON_CONTAINER_ROOT=" + root}, payload(t, "Stop", "abc123", "/x"), hookArgs("claude-personal")...)

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if !env.Exists(".baton/sessions/claude-abc123.jsonl") || env.Exists(".baton/projects/app/sessions") {
		t.Error("a host hook did not write to the host's sessions directory")
	}
}

func TestSandboxLs_ASessionInTheContainerShowsUpWithItsRuntimeAndLiveStatus(t *testing.T) {
	env := newEnv(t)
	repo, _ := launchProject(t, env)
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	newOK(t, env)
	root := containerRoot(t, env, "app")

	// The container sees the Project at /workspace, which is not where it is
	// on the host.
	sandboxHook(t, env, root, "SessionStart", "s1", "/workspace/pkg")
	waiting := lsRows(t, env)
	sandboxHook(t, env, root, "PreToolUse", "s1", "/workspace/pkg")
	running := lsRows(t, env)

	want := func(status string) [][]string {
		return [][]string{{"app", "YYYY-MM-DD-1", "claude", "s1", "app.claude", "sandbox", status}}
	}
	if !reflect.DeepEqual(waiting, want("waiting_input")) {
		t.Errorf("rows = %v, want %v", waiting, want("waiting_input"))
	}
	if !reflect.DeepEqual(running, want("running")) {
		t.Errorf("rows = %v, want %v", running, want("running"))
	}
	if got := env.ListDir(".baton/projects"); !reflect.DeepEqual(got, []string{"app"}) {
		t.Errorf("projects = %v, want only app, not a Project at the container's path", got)
	}
}

func TestSandboxLs_PathsAreMappedThroughTheMountsNotAssumedEqual(t *testing.T) {
	// The directories exist on the host, in the Project, so that the Project
	// would be found if the container's path were taken for a host path.
	cases := []struct {
		name, cwd, project string
	}{
		{"the Project's mount", "/workspace", "app"},
		{"the home mount", "/home/sandbox/scratch", "app"},
		{"a mounted file's parent", "/workspace/.ai-sandbox/hooks", "app"},
		{"the container's own tmpfs", "/tmp", "-"},
		{"the Baton directory, which is in no repository", "/baton/project", "-"},
		{"a host path the container does not have", "$REPO", "-"},
		{"a path that climbs out of its mount", "/workspace/../tmp", "-"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newEnv(t)
			repo, home := launchProject(t, env)
			for _, d := range []string{filepath.Join(home, "scratch"), filepath.Join(repo, ".ai-sandbox", "hooks")} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			newOK(t, env)
			root := containerRoot(t, env, "app")
			cwd := strings.ReplaceAll(tc.cwd, "$REPO", repo)

			sandboxHook(t, env, root, "Stop", "s1", cwd)

			rows := lsRows(t, env)
			if len(rows) != 1 || rows[0][0] != tc.project || rows[0][5] != "sandbox" {
				t.Errorf("rows = %v, want one sandbox Session of %s", rows, tc.project)
			}
		})
	}
}

func TestSandboxLs_ASessionOfAContainerBatonDidNotStartHasNoProject(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	root := containerRoot(t, env, "app") // no `baton new`: the mounts of the container are not known

	sandboxHook(t, env, root, "Stop", "s1", "/workspace")

	rows := lsRows(t, env)
	if len(rows) != 1 || rows[0][0] != "-" || rows[0][5] != "sandbox" {
		t.Errorf("rows = %v, want one sandbox Session without a Project", rows)
	}
}

func TestSandboxLs_ASandboxSessionBlocksAnotherInItsWorkingCopyUntilItEnds(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	newOK(t, env)
	root := containerRoot(t, env, "app")
	sandboxHook(t, env, root, "SessionStart", "s1", "/workspace")

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "already has an active session: claude, profile app.claude, session s1") {
		t.Errorf("exit code = %d, stderr=%q, want the active sandbox Session named", res.ExitCode, res.Stderr)
	}
	sandboxHook(t, env, root, "SessionEnd", "s1", "/workspace")
	newOK(t, env)
}

func TestSandboxNew_AGitWorktreeIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	env := newEnv(t)
	repo, _ := launchProject(t, env)
	wt := addWorktree(t, env, repo, "app-feature")

	res := env.Run("new", wt, "--agent", "claude")

	want := "baton: project app runs in the sandbox Runtime, whose Sessions run only in the main checkout " + repo + ", not in the worktree " + wt + "\n"
	if res.ExitCode != 1 || res.Stderr != want {
		t.Errorf("exit code = %d, stderr=%q, want %q", res.ExitCode, res.Stderr, want)
	}
	if calls := env.Calls("guake"); len(calls) != 0 {
		t.Errorf("guake was called: %v", calls)
	}
	if calls := env.Calls("docker"); len(calls) != 0 {
		t.Errorf("docker was called: %v", calls)
	}
	if env.Exists(".baton/projects/app/current.jsonl") || env.Exists(".baton/projects/app/tasks") {
		t.Error("a Task was made for the refused Session")
	}
}

func TestSandboxNew_WithoutTheBatonBinaryToMountIsRefusedWithAHint(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	if err := os.Remove(env.Path(".baton", "bin", "baton")); err != nil {
		t.Fatal(err)
	}

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, env.Path(".baton", "bin", "baton")+" does not exist; run make install") {
		t.Errorf("exit code = %d, stderr=%q, want a hint to run make install", res.ExitCode, res.Stderr)
	}
	if calls := env.Calls("guake"); len(calls) != 0 {
		t.Errorf("guake was called: %v", calls)
	}
}

func TestSandboxNew_TheSandboxHasItsOwnProfilesSoAHostProfileCannotBeChosen(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)

	res := env.Run("new", "app", "--agent", "claude", "--profile", "claude-personal")

	want := "baton: project app runs in the sandbox Runtime, which has Profiles of its own; --profile applies to host Sessions only\n"
	if res.ExitCode != 1 || res.Stderr != want {
		t.Errorf("exit code = %d, stderr=%q, want %q", res.ExitCode, res.Stderr, want)
	}
	if calls := env.Calls("guake"); len(calls) != 0 {
		t.Errorf("guake was called: %v", calls)
	}
}

func TestSandboxNew_RunShOutputThatIsNotADockerCommandIsReported(t *testing.T) {
	env := newEnv(t)
	repo, _ := launchProject(t, env)
	env.WriteFile(filepath.Join(repo, ".ai-sandbox", "run.sh"), "#!/bin/sh\necho hello\n", true)

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "run.sh printed something other than `docker run") {
		t.Errorf("exit code = %d, stderr=%q, want the unexpected output reported", res.ExitCode, res.Stderr)
	}
	if calls := env.Calls("guake"); len(calls) != 0 {
		t.Errorf("guake was called: %v", calls)
	}
}

func TestSandboxNew_AProjectWhoseRunShIsGoneIsReported(t *testing.T) {
	env := newEnv(t)
	repo, _ := launchProject(t, env)
	if err := os.Remove(filepath.Join(repo, ".ai-sandbox", "run.sh")); err != nil {
		t.Fatal(err)
	}

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode != 1 || res.Stderr != "baton: project app has no .ai-sandbox/run.sh\n" {
		t.Errorf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
}

func TestSandboxNew_RunShRenderingTheConfigsDoesNotTouchWhatSyncOwns(t *testing.T) {
	env := newEnv(t)
	fragmentSource(env, "Be brief.\n", "npx")
	env.SourceFile("skills/greet/SKILL.md", "---\nname: greet\ndescription: Say hello\n---\nHello.\n")
	env.CommitSource("skill")
	_, home := launchProject(t, env)
	syncOK(t, env)
	before := readTree(t, filepath.Join(home, ".claude"))
	hooks := env.ReadFile(filepath.Join(home, ".codex", "hooks.json"))

	newOK(t, env)

	if !reflect.DeepEqual(readTree(t, filepath.Join(home, ".claude")), before) || env.ReadFile(filepath.Join(home, ".codex", "hooks.json")) != hooks {
		t.Error("starting the sandbox changed the Claude Profile or the Codex hooks")
	}
	// And the next sync still finds everything it owns as it left it.
	res := env.Run("sync")
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "app.codex: 0 files written, 0 removed") {
		t.Errorf("sync after start: exit code = %d, stdout=%q, stderr=%q, want nothing to write", res.ExitCode, res.Stdout, res.Stderr)
	}
}

// writtenSandboxHook syncs the Project and returns the Stop hook command of
// the sandbox Profile of the Agent as sync wrote it, with the path of the
// Baton binary in the container (which cannot exist on the test machine)
// replaced by binary.
func writtenSandboxHook(t *testing.T, env *batontest.Env, home, agentName, binary string) string {
	t.Helper()
	syncOK(t, env)
	file := map[string]string{"claude": filepath.Join(home, ".claude", "settings.json"), "codex": filepath.Join(home, ".codex", "hooks.json")}[agentName]
	cmds := hookCommands(t, env, file)["Stop"]
	if len(cmds) != 1 || !strings.Contains(cmds[0], "/baton/bin/baton") {
		t.Fatalf("Stop hook commands = %q, want one calling /baton/bin/baton", cmds)
	}
	return strings.ReplaceAll(cmds[0], "/baton/bin/baton", binary)
}

func TestSandboxHook_TheWrittenCommandDoesNothingAndSucceedsWhenTheContainerHasNoBatonBinary(t *testing.T) {
	for _, agentName := range []string{"claude", "codex"} {
		t.Run(agentName, func(t *testing.T) {
			env := newEnv(t)
			_, home := launchProject(t, env)
			command := writtenSandboxHook(t, env, home, agentName, env.Path("no", "such", "baton"))

			res := env.RunProgram(nil, payload(t, "Stop", "abc123", "/workspace"), "sh", "-c", command)

			if res.ExitCode != 0 || res.Stdout != "" || res.Stderr != "" {
				t.Errorf("exit code = %d, stdout=%q, stderr=%q, want a silent success", res.ExitCode, res.Stdout, res.Stderr)
			}
			if env.Exists(".baton/projects/app/sessions") || env.Exists(".baton/sessions") {
				t.Error("an Event was written")
			}
		})
	}
}

func TestSandboxHook_TheWrittenCommandRecordsTheEventWhenTheBinaryIsThere(t *testing.T) {
	env := newEnv(t)
	_, home := launchProject(t, env)
	root := containerRoot(t, env, "app")
	command := writtenSandboxHook(t, env, home, "claude", batonBin)

	res := env.RunProgram([]string{"BATON_CONTAINER_ROOT=" + root}, payload(t, "Stop", "abc123", "/workspace"), "sh", "-c", command)

	if res.ExitCode != 0 || res.Stdout != "" || res.Stderr != "" {
		t.Fatalf("exit code = %d, stdout=%q, stderr=%q, want a silent success", res.ExitCode, res.Stdout, res.Stderr)
	}
	lines := eventLines(t, env, ".baton/projects/app/sessions/claude-abc123.jsonl")
	if len(lines) != 1 || lines[0]["event"] != "Stop" || lines[0]["runtime"] != "sandbox" || lines[0]["profile"] != "app.claude" {
		t.Errorf("lines = %v, want one sandbox Stop Event of app.claude", lines)
	}
}

func TestSandboxHook_TheGuardLeavesNoShellBetweenTheAgentAndTheHook(t *testing.T) {
	env := newEnv(t)
	_, home := launchProject(t, env)
	root := containerRoot(t, env, "app")
	command := writtenSandboxHook(t, env, home, "claude", batonBin)
	pidFile := env.Path("claude.pid")
	env.WriteFile("payload.json", payload(t, "SessionStart", "abc123", "/workspace"))
	// Claude Code runs a hook command with sh -c; the stub plays that.
	env.Stub("claude", batontest.WithScript(`echo $$ > "`+pidFile+`"
export BATON_CONTAINER_ROOT="`+root+`"
sh -c '`+strings.ReplaceAll(command, `'`, `'\''`)+`' < "`+env.Path("payload.json")+`"`))

	if res := env.Command("claude"); res.ExitCode != 0 {
		t.Fatalf("claude stub: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	line := eventLines(t, env, ".baton/projects/app/sessions/claude-abc123.jsonl")[0]
	wantPID, _ := strconv.Atoi(strings.TrimSpace(env.ReadFile(pidFile)))
	if wantPID == 0 || line["agent_pid"] != float64(wantPID) {
		t.Errorf("agent_pid = %v, want the claude process %d", line["agent_pid"], wantPID)
	}
	if _, fallback := line["agent_pid_fallback"]; fallback {
		t.Error("agent_pid_fallback is set: the Agent was not found among the ancestors")
	}
}
