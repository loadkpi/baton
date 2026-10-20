package test

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/loadkpi/baton/internal/batontest"
)

// composeProject creates a git repository ~/work/<name> that is a registered
// Project with a host Runtime, as one is after a first Session, and returns its
// path. The Compose setup is the fake docker of the Env.
func composeProject(t *testing.T, env *batontest.Env, name string) string {
	t.Helper()
	repo := newRepo(t, env, name)
	env.WriteFile(filepath.Join(repo, "docker-compose.yml"), "services: {}\n")
	env.WriteFile(".baton/projects/"+name+"/project.toml", "path = \""+repo+"\"\nruntime = \"host\"\n")
	return repo
}

// devContainer adds the fake container of service "app" the way a dev setup
// has it: user 1001:1001, HOME /home/dev, a named volume at /state and
// ~/.claude a link into it.
func devContainer(env *batontest.Env, running bool) *batontest.Container {
	c := env.AddContainer(batontest.ContainerSpec{
		Service: "app", Running: running, User: "1001:1001", Home: "/home/dev",
		Volumes: map[string]string{"app-state": "/state"},
	})
	c.MkdirAll("/state/.claude")
	c.Symlink("/home/dev/.claude", "/state/.claude")
	return c
}

// composeConfig is the standard config plus the Compose service of Project
// "app" with one Claude Profile at ~/.claude of the container.
func composeConfig(env *batontest.Env, tags string) string {
	return standardConfig(env) + `
[projects.app.compose]
files   = ["docker-compose.yml"]
service = "app"

[projects.app.compose.profiles.claude]
agent = "claude"
dir   = "~/.claude"
` + tags
}

// composeHookCommand is the command sync writes into a Profile in a Compose
// container: the binary where the service must mount it, guarded.
func composeHookCommand(profile string) string {
	return "[ -x /baton/bin/baton ] || exit 0; exec /baton/bin/baton hook claude --profile " + profile + " --runtime compose"
}

// composeDockerCalls are the recorded docker invocations, one line each.
func composeDockerCalls(env *batontest.Env) []string {
	var out []string
	for _, c := range env.Calls("docker") {
		out = append(out, strings.Join(c.Args, " "))
	}
	return out
}

// startsOrChangesAService is true for a docker call that would start, create,
// stop or remove a container of the Compose setup.
func startsOrChangesAService(call string) bool {
	for _, verb := range []string{" up", " start", " create", " restart", " stop", " down", " rm ", " kill", " pull", " build"} {
		if strings.HasPrefix(call, "compose") && strings.Contains(call, verb) {
			return true
		}
		if strings.HasPrefix(call, "start ") || strings.HasPrefix(call, "restart ") || strings.HasPrefix(call, "stop ") || strings.HasPrefix(call, "rm ") {
			return true
		}
	}
	return false
}

func TestComposeSync_RunningServiceGetsSkillsAndHooksInTheRealConfigDirectory(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(composeConfig(env, ""))
	env.SourceFile("skills/greet/SKILL.md", "---\nname: greet\ndescription: Say hello\n---\nHello.\n")
	env.SourceFile("skills/greet/scripts/hello.sh", "#!/bin/sh\necho hello\n", true)
	env.CommitSource("add skill")
	repo := composeProject(t, env, "app")
	c := devContainer(env, true)

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	// The files are in the directory the link points to, not next to it.
	if got, want := c.ReadFile("/state/.claude/skills/greet/SKILL.md"), "---\nname: greet\ndescription: Say hello\n---\nHello.\n"; got != want {
		t.Errorf("skill = %q, want %q", got, want)
	}
	if c.Mode("/state/.claude/skills/greet/scripts/hello.sh")&0o100 == 0 {
		t.Error("the script lost its execute bit")
	}
	if got := c.ListDir("/state"); !reflect.DeepEqual(got, []string{".claude"}) {
		t.Errorf("/state holds %v, want only .claude", got)
	}
	want := make(map[string][]string)
	for _, event := range statusEvents {
		want[event] = []string{composeHookCommand("app.claude")}
	}
	if got := hookCommandsOf(t, c.ReadFile("/state/.claude/settings.json"), "settings.json"); !reflect.DeepEqual(got, want) {
		t.Errorf("hooks = %v, want %v", got, want)
	}
	if !strings.Contains(res.Stdout, "app.claude: 4 files written, 0 removed, 0 unchanged") || !strings.Contains(res.Stdout, "app.claude: status hooks installed") {
		t.Errorf("stdout = %q, want the usual lines for the Profile app.claude", res.Stdout)
	}

	// Through the running service, as its user, with the Project's Compose file.
	calls := composeDockerCalls(env)
	file := filepath.Join(repo, "docker-compose.yml")
	if !slices.Contains(calls, "compose -f "+file+" ps -a -q app") {
		t.Errorf("docker calls = %q, want the service looked up with compose -f %s", calls, file)
	}
	if !slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "compose -f "+file+" exec -T app ") }) {
		t.Errorf("docker calls = %q, want compose exec -T app", calls)
	}
	for _, call := range calls {
		if startsOrChangesAService(call) || strings.HasPrefix(call, "run ") {
			t.Errorf("docker call %q changes a container or starts a new one", call)
		}
	}
}

func TestComposeSync_StoppedServiceIsSyncedThroughItsVolumeAsTheContainersUser(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(composeConfig(env, ""))
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.CommitSource("add skill")
	composeProject(t, env, "app")
	c := devContainer(env, false)

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := c.ReadFile("/state/.claude/skills/greet/SKILL.md"); got != "Hello.\n" {
		t.Errorf("skill = %q, want the source version", got)
	}
	if !strings.Contains(c.ReadFile("/state/.claude/settings.json"), composeHookCommand("app.claude")) {
		t.Error("the hooks are not in the volume's .claude")
	}
	if got := c.ListDir("/state"); !reflect.DeepEqual(got, []string{".claude"}) {
		t.Errorf("/state holds %v, want only .claude", got)
	}

	var runs int
	for _, call := range composeDockerCalls(env) {
		switch {
		case startsOrChangesAService(call):
			t.Errorf("docker call %q starts or changes the service", call)
		case strings.HasPrefix(call, "compose") && strings.Contains(call, " exec "):
			t.Errorf("docker call %q execs into a service that is stopped", call)
		case strings.HasPrefix(call, "run "):
			runs++
			for _, want := range []string{"--rm", "-i", "--network=none", "--user 1001:1001", "--entrypoint sh", "-v app-state:/v", " app-image "} {
				if !strings.Contains(call, want) {
					t.Errorf("docker call %q lacks %q", call, want)
				}
			}
		}
	}
	if runs == 0 {
		t.Error("no one-shot docker run wrote into the volume")
	}
}

func TestComposeSync_InstructionsAndMCPServersLandWhereClaudeReadsThem(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(composeConfig(env, ""))
	env.SourceFile("AGENTS.md", "Be brief.\n")
	env.SourceFile("mcp.json", sentryMCP)
	env.CommitSource("add instructions and mcp")
	writeSecrets(t, env, 0o600, "SENTRY_TOKEN=s3cret-token\n")
	composeProject(t, env, "app")
	c := devContainer(env, true)

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := c.ReadFile("/state/.claude/CLAUDE.md"); !strings.Contains(got, "Be brief.") || !strings.Contains(got, "<!-- baton:begin -->") {
		t.Errorf("CLAUDE.md = %q, want Baton's block with the instructions", got)
	}
	// ~/.claude.json is next to ~/.claude, which is what Claude reads when
	// CLAUDE_CONFIG_DIR is not set; it holds a secret, so it is private.
	if got := c.ReadFile("/home/dev/.claude.json"); !strings.Contains(got, "Bearer s3cret-token") {
		t.Errorf(".claude.json = %q, want the server with the secret substituted", got)
	}
	if mode := c.Mode("/home/dev/.claude.json"); mode != 0o600 {
		t.Errorf(".claude.json mode = %v, want 0600", mode)
	}
	if c.Exists("/state/.claude.json") {
		t.Error("an MCP configuration was written next to the volume's directory")
	}
	if strings.Contains(res.Stdout+res.Stderr, "s3cret-token") {
		t.Errorf("output shows the secret: %q %q", res.Stdout, res.Stderr)
	}
	if got := manifestKeys(t, env, "app.claude"); got[".claude.json"] == "" && len(got) == 0 {
		t.Errorf("manifest keys = %v, want the server recorded", got)
	}
}

func TestComposeSync_StoppedServiceCannotWriteOutsideItsVolumesAndSaysSo(t *testing.T) {
	env := newEnv(t)
	hostProfile := env.MakeProfileDir(".claude-personal")
	env.WriteConfig(composeConfig(env, ""))
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.SourceFile("mcp.json", `{"mcpServers": {"plain": {"type": "http", "url": "https://x.example.test/mcp"}}}`)
	env.CommitSource("add skill and mcp")
	composeProject(t, env, "app")
	c := devContainer(env, false)

	res := env.Run("sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	for _, want := range []string{"app.claude", "project app", "service app", "/home/dev/.claude.json", "volume"} {
		if !strings.Contains(res.Stderr, want) {
			t.Errorf("stderr = %q, want it to mention %q", res.Stderr, want)
		}
	}
	if c.Exists("/home/dev/.claude.json") {
		t.Error("a file was written into the container's own layer")
	}
	if !env.Exists(filepath.Join(hostProfile, "skills/greet/SKILL.md")) {
		t.Error("the host Profile was not synced although the compose Profile failed")
	}
}

func TestComposeSync_AMcpFileLinkedIntoTheVolumeIsWrittenThereWhileStopped(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(composeConfig(env, ""))
	env.SourceFile("mcp.json", sentryMCP)
	env.CommitSource("add mcp")
	writeSecrets(t, env, 0o600, "SENTRY_TOKEN=s3cret-token\n")
	composeProject(t, env, "app")
	c := devContainer(env, false)
	c.WriteFile("/state/.claude.json", "{\"theme\": \"dark\"}\n")
	c.Chmod("/state/.claude.json", 0o600)
	c.Symlink("/home/dev/.claude.json", "/state/.claude.json")

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	got := c.ReadFile("/state/.claude.json")
	if !strings.Contains(got, "Bearer s3cret-token") || !strings.Contains(got, `"theme": "dark"`) {
		t.Errorf(".claude.json = %q, want the server added and the rest kept", got)
	}
	if !c.Exists("/home/dev/.claude.json") || c.ListDir("/home/dev") == nil {
		t.Error("the link was replaced")
	}
}

func TestComposeSync_SecretsAreLimitedByTheTagsOfTheProfile(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(composeConfig(env, `tags = ["personal"]`+"\n"))
	env.SourceFile("mcp.json", `{"mcpServers": {
  "tracker": {"tags": ["work"], "type": "http", "url": "https://t.example.test/mcp", "headers": {"Authorization": "Bearer ${WORK_TOKEN}"}},
  "home": {"tags": ["personal"], "type": "http", "url": "https://h.example.test/mcp"}
}}`)
	env.SourceFile("skills/review/SKILL.md", "---\ntags: [work]\n---\nReview.\n")
	env.SourceFile("skills/diary/SKILL.md", "---\ntags: [personal]\n---\nDiary.\n")
	env.CommitSource("add tagged items")
	composeProject(t, env, "app")
	c := devContainer(env, true)

	// WORK_TOKEN is not in the secrets file: the work server is not for this
	// Profile, so nothing needs it.
	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := c.ReadFile("/home/dev/.claude.json"); !strings.Contains(got, "h.example.test") || strings.Contains(got, "t.example.test") {
		t.Errorf(".claude.json = %q, want only the personal server", got)
	}
	if !c.Exists("/state/.claude/skills/diary/SKILL.md") || c.Exists("/state/.claude/skills/review") {
		t.Errorf("skills = %v, want only the personal one", c.ListDir("/state/.claude/skills"))
	}
}

// foreignGreetInContainer sets up the question tests: a hand-made skill
// "greet" in the container's Profile that Baton does not own.
func foreignGreetInContainer(t *testing.T, env *batontest.Env, running bool) *batontest.Container {
	t.Helper()
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(composeConfig(env, ""))
	env.SourceFile("skills/greet/SKILL.md", "Hello from the source.\n")
	env.CommitSource("add skill")
	composeProject(t, env, "app")
	c := devContainer(env, running)
	c.WriteFile("/state/.claude/skills/greet/SKILL.md", "Hand-made greeting.\n")
	c.WriteFile("/state/.claude/skills/greet/notes.txt", "my notes\n")
	return c
}

func TestComposeSync_ForeignSkillIsShownAsDiffBackedUpOnTheHostAndReplacedOnYes(t *testing.T) {
	for _, running := range []bool{true, false} {
		name := "stopped"
		if running {
			name = "running"
		}
		t.Run(name, func(t *testing.T) {
			env := newEnv(t)
			c := foreignGreetInContainer(t, env, running)

			res := env.RunTerminal([]string{"y"}, "sync")

			if res.ExitCode != 0 {
				t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
			}
			for _, want := range []string{"/home/dev/.claude/skills/greet", "-Hand-made greeting.", "+Hello from the source.", "notes.txt"} {
				if !strings.Contains(res.Stdout, want) {
					t.Errorf("transcript lacks %q:\n%s", want, res.Stdout)
				}
			}
			if got := c.ReadFile("/state/.claude/skills/greet/SKILL.md"); got != "Hello from the source.\n" {
				t.Errorf("SKILL.md = %q, want the source version", got)
			}
			if c.Exists("/state/.claude/skills/greet/notes.txt") {
				t.Error("the foreign extra file is still in the replaced skill")
			}
			backup := onlyBackup(t, env, "app.claude")
			if got := env.ReadFile(filepath.Join(backup, "skills/greet/SKILL.md")); got != "Hand-made greeting.\n" {
				t.Errorf("backed up SKILL.md = %q, want the hand-made version", got)
			}
			if got := env.ReadFile(filepath.Join(backup, "skills/greet/notes.txt")); got != "my notes\n" {
				t.Errorf("backed up notes.txt = %q, want the hand-made version", got)
			}
		})
	}
}

func TestComposeSync_AnsweringNoOrHavingNoTerminalChangesNothingInTheContainer(t *testing.T) {
	env := newEnv(t)
	c := foreignGreetInContainer(t, env, true)

	noTerminal := env.RunWithStdin("y\n", "sync")
	no := env.RunTerminal([]string{"n"}, "sync")

	if noTerminal.ExitCode == 0 || no.ExitCode == 0 {
		t.Fatalf("exit codes = %d and %d, want both non-zero", noTerminal.ExitCode, no.ExitCode)
	}
	if !strings.Contains(noTerminal.Stderr, "/home/dev/.claude/skills/greet") || !strings.Contains(noTerminal.Stderr, "terminal") {
		t.Errorf("stderr = %q, want it to name the container path and say there is no terminal", noTerminal.Stderr)
	}
	if got := c.ReadFile("/state/.claude/skills/greet/SKILL.md"); got != "Hand-made greeting.\n" {
		t.Errorf("hand-made SKILL.md = %q, want it untouched", got)
	}
	if c.Exists("/state/.claude/settings.json") || c.Exists("/state/.claude/skills/baton-handoff") {
		t.Error("sync wrote other files although nobody agreed")
	}
	if env.Exists(".baton/manifests/app.claude.json") || env.Exists(".baton/backups") {
		t.Error("sync recorded a manifest or a backup although it stopped")
	}
}

func TestComposeSync_ABlockEditedByHandAsksAgainAndOtherContentSurvives(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(composeConfig(env, ""))
	env.SourceFile("AGENTS.md", "Be brief.\n")
	env.CommitSource("add instructions")
	composeProject(t, env, "app")
	c := devContainer(env, true)
	c.WriteFile("/state/.claude/CLAUDE.md", "My own notes.\n")

	if res := env.RunTerminal([]string{"y"}, "sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	first := c.ReadFile("/state/.claude/CLAUDE.md")
	if !strings.HasPrefix(first, "My own notes.\n") || !strings.Contains(first, "Be brief.") {
		t.Fatalf("CLAUDE.md = %q, want the user's text and Baton's block", first)
	}
	c.WriteFile("/state/.claude/CLAUDE.md", strings.Replace(first, "Be brief.", "Be very brief.", 1))

	res := env.Run("sync")

	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "/home/dev/.claude/CLAUDE.md") {
		t.Fatalf("exit code = %d, stderr=%q, want an error naming the file after a hand edit without a terminal", res.ExitCode, res.Stderr)
	}
	if got := c.ReadFile("/state/.claude/CLAUDE.md"); !strings.Contains(got, "Be very brief.") {
		t.Errorf("CLAUDE.md = %q, want the hand edit untouched", got)
	}
}

func TestComposeSync_ManifestStaysOnTheHostAndRemovalAndRerunFollowIt(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(composeConfig(env, ""))
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.SourceFile("skills/greet/scripts/hello.sh", "#!/bin/sh\n", true)
	env.SourceFile("skills/review/SKILL.md", "Review.\n")
	env.CommitSource("add skills")
	composeProject(t, env, "app")
	c := devContainer(env, true)
	c.WriteFile("/state/.claude/skills/theirs/SKILL.md", "Not Baton's.\n")

	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := []string{"skills/baton-handoff/SKILL.md", "skills/greet/SKILL.md", "skills/greet/scripts/hello.sh", "skills/review/SKILL.md"}
	if got := manifestPaths(t, env, "app.claude"); !slices.Equal(got, want) {
		t.Errorf("manifest files = %v, want %v", got, want)
	}
	if c.Exists("/state/.claude/.baton") || len(c.ListDir("/state")) != 1 {
		t.Errorf("/state holds %v, want only .claude (the manifest is on the host)", c.ListDir("/state"))
	}

	again := env.Run("sync")
	if again.ExitCode != 0 || !strings.Contains(again.Stdout, "app.claude: 0 files written, 0 removed, ") {
		t.Errorf("second sync: exit %d, stdout=%q, want nothing written", again.ExitCode, again.Stdout)
	}

	env.Command("git", "-C", env.SourceDir, "rm", "-rq", "skills/greet")
	env.CommitSource("drop greet")
	dropped := env.Run("sync")
	if dropped.ExitCode != 0 {
		t.Fatalf("third sync: exit code = %d, stderr=%q", dropped.ExitCode, dropped.Stderr)
	}
	if c.Exists("/state/.claude/skills/greet") {
		t.Error("the skill the source dropped is still in the container (not even its empty directory should stay)")
	}
	if got := c.ReadFile("/state/.claude/skills/theirs/SKILL.md"); got != "Not Baton's.\n" {
		t.Errorf("a skill Baton does not own = %q, want it untouched", got)
	}
	if !c.Exists("/state/.claude/skills") {
		t.Error("skills/ itself was removed")
	}
}

func TestComposeSync_BatonNeverEditsTheProjectsComposeFiles(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(composeConfig(env, ""))
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.CommitSource("add skill")
	repo := composeProject(t, env, "app")
	devContainer(env, true)
	before := env.ReadFile(filepath.Join(repo, "docker-compose.yml"))

	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	if got := env.ReadFile(filepath.Join(repo, "docker-compose.yml")); got != before {
		t.Errorf("docker-compose.yml = %q, want it unchanged", got)
	}
	if got := env.ListDir(repo); !slices.Equal(got, []string{".git", "docker-compose.yml"}) {
		t.Errorf("the Project holds %v, want no new file", got)
	}
}

func TestComposeSync_AVolumeSharedByTwoSetupsIsConfiguredAndSyncedOnce(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	// Two Compose setups mount the same volume at /state. It is configured once,
	// as a top-level Profile hosted by Project "app".
	env.WriteConfig(standardConfig(env) + `
[projects.app.compose]
files   = ["docker-compose.yml"]
service = "app"

[projects.other.compose]
files   = ["docker-compose.yml"]
service = "worker"

[projects.other.compose.profiles.codexless]
agent = "claude"
dir   = "/opt/tools/.claude"

[profiles.shared-claude]
runtime = "compose"
project = "app"
agent   = "claude"
dir     = "~/.claude"
`)
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.CommitSource("add skill")
	composeProject(t, env, "app")
	composeProject(t, env, "other")
	app := devContainer(env, true)
	worker := env.AddContainer(batontest.ContainerSpec{Service: "worker", Running: true, Home: "/home/dev", Volumes: map[string]string{"app-state": "/state"}})
	worker.MkdirAll("/opt/tools/.claude")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := strings.Count(res.Stdout, "shared-claude: "); got != 2 { // the summary and the hooks note
		t.Errorf("stdout = %q, want the shared Profile reported once", res.Stdout)
	}
	if !app.Exists("/state/.claude/skills/greet/SKILL.md") {
		t.Error("the shared Profile was not synced through the Project that hosts it")
	}
	if !env.Exists(".baton/manifests/shared-claude.json") || env.Exists(".baton/manifests/other.shared-claude.json") {
		t.Error("the shared Profile has no single manifest")
	}
	for _, call := range composeDockerCalls(env) {
		if strings.Contains(call, " exec -T worker ") && strings.Contains(call, "/state") {
			t.Errorf("docker call %q reaches the shared volume through the second setup", call)
		}
	}
	if !worker.Exists("/opt/tools/.claude/skills/greet/SKILL.md") {
		t.Error("the Profile of the second Project was not synced in its own container")
	}
}

func TestComposeSync_AnUnreachableServiceFailsItsProfilesAndNotTheOthers(t *testing.T) {
	cases := map[string]func(env *batontest.Env){
		"no container": func(env *batontest.Env) {},
		"no directory": func(env *batontest.Env) {
			c := env.AddContainer(batontest.ContainerSpec{Service: "app", Running: true})
			c.MkdirAll("/home/dev")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			env := newEnv(t)
			hostProfile := env.MakeProfileDir(".claude-personal")
			env.WriteConfig(composeConfig(env, ""))
			env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
			env.CommitSource("add skill")
			composeProject(t, env, "app")
			setup(env)

			res := env.Run("sync")

			if res.ExitCode == 0 {
				t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
			}
			for _, want := range []string{"app.claude", "project app", "service app"} {
				if !strings.Contains(res.Stderr, want) {
					t.Errorf("stderr = %q, want it to mention %q", res.Stderr, want)
				}
			}
			if !env.Exists(filepath.Join(hostProfile, "skills/greet/SKILL.md")) || !strings.Contains(res.Stdout, "claude-personal: ") {
				t.Errorf("the host Profile was not synced; stdout=%q", res.Stdout)
			}
		})
	}
}

func TestComposeSync_AVolumeThatIsGoneFailsTheProfileNamingProjectAndService(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(composeConfig(env, ""))
	composeProject(t, env, "app")
	c := devContainer(env, false)
	// Docker's `run -v name:/v` would create a missing volume and write into
	// it; the fake has none by that name.
	env.RemoveFakeVolume("app-state")
	_ = c

	res := env.Run("sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	for _, want := range []string{"app.claude", "project app", "service app", "app-state"} {
		if !strings.Contains(res.Stderr, want) {
			t.Errorf("stderr = %q, want it to mention %q", res.Stderr, want)
		}
	}
	for _, call := range composeDockerCalls(env) {
		if strings.HasPrefix(call, "run ") {
			t.Errorf("docker call %q would create the missing volume", call)
		}
	}
}

func TestComposeConfig_MistakesStopBatonWithAMessageNamingTheKey(t *testing.T) {
	cases := []struct{ name, extra, want string }{
		{"no service", "[projects.x.compose]\nfiles = [\"c.yml\"]\n[projects.x.compose.profiles.c]\nagent = \"claude\"\ndir = \"/c\"\n", "projects.x.compose: service is required"},
		{"no files", "[projects.x.compose]\nservice = \"s\"\n[projects.x.compose.profiles.c]\nagent = \"claude\"\ndir = \"/c\"\n", "projects.x.compose: files is required"},
		{"no profiles", "[projects.x.compose]\nfiles = [\"c.yml\"]\nservice = \"s\"\n", "projects.x.compose: no Profiles"},
		{"relative dir", "[projects.x.compose]\nfiles = [\"c.yml\"]\nservice = \"s\"\n[projects.x.compose.profiles.c]\nagent = \"claude\"\ndir = \"rel/.claude\"\n", "profile x.c: dir \"rel/.claude\" must be an absolute path in the container"},
		{"codex", "[projects.x.compose]\nfiles = [\"c.yml\"]\nservice = \"s\"\n[projects.x.compose.profiles.c]\nagent = \"codex\"\ndir = \"/c\"\n", "profile x.c: agent \"codex\" is not supported in a compose container yet"},
		{"shared without project", "[profiles.s]\nruntime = \"compose\"\nagent = \"claude\"\ndir = \"/c\"\n", "profile s: project is required"},
		{"shared with unknown project", "[profiles.s]\nruntime = \"compose\"\nproject = \"nope\"\nagent = \"claude\"\ndir = \"/c\"\n", "project \"nope\" has no [projects.nope.compose] table"},
		{"project on a host profile", "[profiles.h]\nproject = \"x\"\nagent = \"claude\"\ndir = \"/c\"\n", "profile h: project applies only to runtime"},
		{"unknown runtime", "[profiles.h]\nruntime = \"sandbox\"\nagent = \"claude\"\ndir = \"/c\"\n", "profile h: runtime \"sandbox\" must be"},
		{"unknown key", "[projects.x.compose]\nfiles = [\"c.yml\"]\nservise = \"s\"\n", "unknown key \"projects.x.compose.servise\""},
		{"same name twice", "[projects.x.compose]\nfiles = [\"c.yml\"]\nservice = \"s\"\n[projects.x.compose.profiles.c]\nagent = \"claude\"\ndir = \"/c\"\n[profiles.\"x.c\"]\nagent = \"claude\"\ndir = \"/d\"\n", "profile x.c is configured twice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newEnv(t)
			env.MakeProfileDir(".claude-personal")
			env.WriteConfig(standardConfig(env) + "\n" + tc.extra)

			res := env.Run("sync")

			if res.ExitCode != 1 || !strings.Contains(res.Stderr, tc.want) {
				t.Errorf("exit code = %d, stderr = %q, want exit 1 and %q", res.ExitCode, res.Stderr, tc.want)
			}
		})
	}
}

func TestComposeSync_ADirectoryGivenAsAnAbsolutePathNeedsNoHome(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env) + `
[projects.app.compose]
files   = ["docker-compose.yml"]
service = "app"

[projects.app.compose.profiles.claude]
agent = "claude"
dir   = "/state/.claude"
`)
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.CommitSource("add skill")
	composeProject(t, env, "app")
	c := devContainer(env, false)

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := c.ReadFile("/state/.claude/skills/greet/SKILL.md"); got != "Hello.\n" {
		t.Errorf("skill = %q, want the source version", got)
	}
}

func TestComposeHook_WritesTheEventIntoTheMountedProjectDirectoryOrDoesNothing(t *testing.T) {
	env := newEnv(t)
	root := containerRoot(t, env, "app")
	args := []string{"hook", "claude", "--profile", "app.claude", "--runtime", "compose"}

	res := env.RunWithEnv([]string{"BATON_CONTAINER_ROOT=" + root}, payload(t, "Stop", "abc123", "/workspace"), args...)

	if res.ExitCode != 0 || res.Stdout != "" || res.Stderr != "" {
		t.Fatalf("exit code = %d, stdout=%q, stderr=%q, want a silent success", res.ExitCode, res.Stdout, res.Stderr)
	}
	lines := eventLines(t, env, ".baton/projects/app/sessions/claude-abc123.jsonl")
	if len(lines) != 1 || lines[0]["runtime"] != "compose" || lines[0]["profile"] != "app.claude" || lines[0]["event"] != "Stop" {
		t.Errorf("lines = %v, want one compose Stop Event of app.claude", lines)
	}
	if env.Exists(".baton/sessions") {
		t.Error("the Event went to the host's sessions directory")
	}

	// A service whose override file does not mount Baton yet.
	bare := env.RunWithEnv([]string{"BATON_CONTAINER_ROOT=" + t.TempDir()}, payload(t, "Stop", "def456", "/workspace"), args...)
	if bare.ExitCode != 0 || bare.Stderr != "" || env.Exists(".baton/projects/app/sessions/claude-def456.jsonl") {
		t.Errorf("without the mount: exit code = %d, stderr=%q, want a silent success that writes nothing", bare.ExitCode, bare.Stderr)
	}
}

func TestComposeSync_SecretsTravelOnStandardInputNeverInTheDockerCommandLine(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(composeConfig(env, ""))
	env.SourceFile("mcp.json", sentryMCP)
	env.CommitSource("add mcp")
	writeSecrets(t, env, 0o600, "SENTRY_TOKEN=s3cret-token\n")
	composeProject(t, env, "app")
	devContainer(env, true)

	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	for _, call := range composeDockerCalls(env) {
		if strings.Contains(call, "s3cret-token") {
			t.Errorf("docker call %q carries the secret", call)
		}
		// The service's own user owns what is written: no other user is asked for.
		if strings.Contains(call, " exec ") && (strings.Contains(call, "--user") || strings.Contains(call, " -u ")) {
			t.Errorf("docker call %q runs as another user than the service's", call)
		}
	}
}
