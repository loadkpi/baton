package test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/loadkpi/baton/internal/batontest"
)

// composeCodexConfig is the standard config plus the Compose service of
// Project "app" with one Codex Profile; extra goes into the Profile's table
// (dir, tags).
func composeCodexConfig(env *batontest.Env, extra string) string {
	return standardConfig(env) + `
[projects.app.compose]
files   = ["docker-compose.yml"]
service = "app"

[projects.app.compose.profiles.codex]
agent = "codex"
` + extra
}

// codexContainer adds the fake container of service "app" the way a dev setup
// has it for Codex: user 1001:1001, HOME /home/dev, a volume at /state with
// ~/.codex a link into it, and a volume for ~/.agents (where Codex reads user
// skills), so that a stopped service can be written too. environment is the
// container's own environment.
func codexContainer(env *batontest.Env, running bool, environment ...string) *batontest.Container {
	c := env.AddContainer(batontest.ContainerSpec{
		Service: "app", Running: running, User: "1001:1001", Home: "/home/dev",
		Volumes: map[string]string{"app-state": "/state", "app-agents": "/home/dev/.agents"},
		Env:     environment,
	})
	c.MkdirAll("/state/.codex")
	c.Symlink("/home/dev/.codex", "/state/.codex")
	return c
}

// codexHookCommand is the command sync writes into hooks.json of a Codex
// Profile in a Compose container: guarded, and wrapped in sh -c.
func codexHookCommand(profile string) string {
	return "sh -c '[ -x /baton/bin/baton ] || exit 0; exec /baton/bin/baton hook codex --profile " + profile + " --runtime compose'"
}

const trackerMCP = `{"mcpServers": {"tracker": {"command": "npx", "args": ["-y", "tracker-mcp"], "env": {"TRACKER_TOKEN": "${TRACKER_TOKEN}"}}}}`

// trackerTable is the table sync writes for trackerMCP.
func trackerTable(token string) string {
	return "[mcp_servers.tracker]\nargs = [\"-y\", \"tracker-mcp\"]\ncommand = \"npx\"\n\n[mcp_servers.tracker.env]\nTRACKER_TOKEN = \"" + token + "\"\n"
}

func TestComposeCodexSync_RunningServiceGetsSkillsInstructionsMCPServersAndHooksWhereCodexReadsThem(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(composeCodexConfig(env, `tags = ["work"]`+"\n"))
	env.SourceFile("AGENTS.md", "Be brief.\n")
	env.SourceFile("skills/greet/SKILL.md", "---\nname: greet\ndescription: Say hello\ntags: [work]\n---\nHello.\n")
	env.SourceFile("skills/greet/scripts/hello.sh", "#!/bin/sh\necho hello\n", true)
	env.SourceFile("mcp.json", `{"mcpServers": {
  "tracker": {"command": "npx", "args": ["-y", "tracker-mcp"], "env": {"TRACKER_TOKEN": "${TRACKER_TOKEN}"}},
  "sentry": {"type": "http", "url": "https://sentry.example.test/mcp", "headers": {"Authorization": "Bearer ${SENTRY_TOKEN}"}}
}}`)
	env.CommitSource("add everything")
	writeSecrets(t, env, 0o600, "TRACKER_TOKEN=t0ken\nSENTRY_TOKEN=s3cret-value\n")
	composeProject(t, env, "app")
	c := codexContainer(env, true)

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	// Skills: in $HOME/.agents/skills, with the `tags` key of the front matter
	// taken out, and the script still executable.
	if got, want := c.ReadFile("/home/dev/.agents/skills/greet/SKILL.md"), "---\nname: greet\ndescription: Say hello\n---\nHello.\n"; got != want {
		t.Errorf("skill = %q, want %q", got, want)
	}
	if c.Mode("/home/dev/.agents/skills/greet/scripts/hello.sh")&0o100 == 0 {
		t.Error("the script lost its execute bit")
	}
	if !c.Exists("/home/dev/.agents/skills/baton-handoff/SKILL.md") {
		t.Error("the built-in baton-handoff skill is not installed")
	}
	// Instructions: a marked block in AGENTS.md of the real directory.
	if got, want := c.ReadFile("/state/.codex/AGENTS.md"), "<!-- baton:begin -->\nBe brief.\n<!-- baton:end -->\n"; got != want {
		t.Errorf("AGENTS.md = %q, want %q", got, want)
	}
	// MCP servers: tables in config.toml, private because of the secrets.
	want := `[mcp_servers.sentry]
url = "https://sentry.example.test/mcp"

[mcp_servers.sentry.http_headers]
Authorization = "Bearer s3cret-value"

` + trackerTable("t0ken")
	if got := c.ReadFile("/state/.codex/config.toml"); got != want {
		t.Errorf("config.toml = %q, want %q", got, want)
	}
	if mode := c.Mode("/state/.codex/config.toml"); mode != 0o600 {
		t.Errorf("config.toml mode = %04o, want 0600", mode)
	}
	// Hooks.
	wantHooks := make(map[string][]string)
	for _, event := range codexStatusEvents {
		wantHooks[event] = []string{codexHookCommand("app.codex")}
	}
	if got := hookCommandsOf(t, c.ReadFile("/state/.codex/hooks.json"), "hooks.json"); !reflect.DeepEqual(got, wantHooks) {
		t.Errorf("hooks = %v, want %v", got, wantHooks)
	}
	// No fragments: nothing regenerates these files.
	if c.Exists("/state/.codex/config.d") || c.Exists("/state/.codex/AGENTS.d") {
		t.Errorf("fragments were written: %v", c.ListDir("/state/.codex"))
	}
	if !strings.Contains(res.Stdout, "app.codex: status hooks installed") || !strings.Contains(res.Stdout, "trust them with /hooks") {
		t.Errorf("stdout = %q, want the hooks note with the /hooks advice", res.Stdout)
	}
	if strings.Contains(res.Stdout+res.Stderr, "s3cret-value") || strings.Contains(res.Stdout+res.Stderr, "t0ken") {
		t.Errorf("output shows a secret: %q %q", res.Stdout, res.Stderr)
	}
	// The manifest owns the two tables, by the checksum of their value.
	keys := manifestKeys(t, env, "app.codex")
	if keys["config.toml mcp_servers.tracker"] == "" || keys["config.toml mcp_servers.sentry"] == "" || len(keys) != 2 {
		t.Errorf("manifest keys = %v, want the two servers of config.toml", keys)
	}
	// Through the running service, as its user; nothing is started.
	for _, call := range composeDockerCalls(env) {
		if startsOrChangesAService(call) || strings.HasPrefix(call, "run ") {
			t.Errorf("docker call %q changes a container or starts a new one", call)
		}
	}
}

func TestComposeCodexSync_AProfileThatAcceptsNoTagGetsNoTaggedItem(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(composeCodexConfig(env, ""))
	env.SourceFile("skills/review/SKILL.md", "---\ntags: [work]\n---\nReview.\n")
	env.SourceFile("mcp.json", `{"mcpServers": {"tracker": {"tags": ["work"], "command": "npx", "env": {"T": "${WORK_TOKEN}"}}}}`)
	env.CommitSource("add tagged items")
	composeProject(t, env, "app")
	c := codexContainer(env, true)

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if c.Exists("/home/dev/.agents/skills/review") || c.Exists("/state/.codex/config.toml") {
		t.Error("a work item reached a Profile without the work tag")
	}
}

func TestComposeCodexSync_TheDirectoryIsTheServicesCodexHomeElseDotCodexInTheHome(t *testing.T) {
	cases := []struct {
		name        string
		environment []string
		extra       string
		wantDir     string
	}{
		{"from the service environment", []string{"CODEX_HOME=/state/codex-home"}, "", "/state/codex-home"},
		{"the home's .codex when the service sets none", nil, "", "/state/.codex"},
		{"a dir in the config wins", []string{"CODEX_HOME=/state/codex-home"}, `dir = "/state/.codex"` + "\n", "/state/.codex"},
	}
	for _, tc := range cases {
		for _, running := range []bool{true, false} {
			name := tc.name + ", stopped"
			if running {
				name = tc.name + ", running"
			}
			t.Run(name, func(t *testing.T) {
				env := newEnv(t)
				env.MakeProfileDir(".claude-personal")
				env.WriteConfig(composeCodexConfig(env, tc.extra))
				env.SourceFile("AGENTS.md", "Be brief.\n")
				env.CommitSource("add instructions")
				composeProject(t, env, "app")
				c := codexContainer(env, running, tc.environment...)
				c.MkdirAll("/state/codex-home")

				res := env.Run("sync")

				if res.ExitCode != 0 {
					t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
				}
				if !c.Exists(tc.wantDir+"/AGENTS.md") || !c.Exists(tc.wantDir+"/hooks.json") {
					t.Errorf("%s holds %v, want AGENTS.md and hooks.json", tc.wantDir, c.ListDir(tc.wantDir))
				}
				// User skills are in the HOME, whatever CODEX_HOME says.
				if !c.Exists("/home/dev/.agents/skills/baton-handoff/SKILL.md") {
					t.Error("the skills are not in ~/.agents/skills")
				}
			})
		}
	}
}

func TestComposeCodexSync_ACodexHomeThatIsNotAbsoluteFailsTheProfileNamingIt(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(composeCodexConfig(env, ""))
	composeProject(t, env, "app")
	c := codexContainer(env, true, "CODEX_HOME=relative/codex")

	res := env.Run("sync")

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "app.codex") || !strings.Contains(res.Stderr, `"relative/codex"`) {
		t.Errorf("exit code = %d, stderr=%q, want the Profile failed, naming the value", res.ExitCode, res.Stderr)
	}
	if c.Exists("/home/dev/.agents") && len(c.ListDir("/home/dev/.agents")) > 0 {
		t.Error("something was written although the directory is unknown")
	}
}

func TestComposeCodexSync_StoppedServiceCannotWriteSkillsOutsideItsVolumesAndSaysSo(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(composeCodexConfig(env, ""))
	composeProject(t, env, "app")
	// ~/.agents is in the container's own layer this time.
	c := env.AddContainer(batontest.ContainerSpec{
		Service: "app", Running: false, User: "1001:1001", Home: "/home/dev",
		Volumes: map[string]string{"app-state": "/state"},
	})
	c.MkdirAll("/state/.codex")
	c.Symlink("/home/dev/.codex", "/state/.codex")

	res := env.Run("sync")

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "/home/dev/.agents/skills") || !strings.Contains(res.Stderr, "start the service") {
		t.Errorf("exit code = %d, stderr=%q, want the path and the advice to start the service", res.ExitCode, res.Stderr)
	}
}

// tomlSetup is a Codex Profile in a running service with the tracker server in
// the source, and the given config.toml (mode 0600) in the container; none
// when existing is empty.
func tomlSetup(t *testing.T, env *batontest.Env, existing string) *batontest.Container {
	t.Helper()
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(composeCodexConfig(env, ""))
	env.SourceFile("mcp.json", trackerMCP)
	env.CommitSource("add mcp")
	writeSecrets(t, env, 0o600, "TRACKER_TOKEN=t0ken\n")
	composeProject(t, env, "app")
	c := codexContainer(env, true)
	if existing != "" {
		c.WriteFile("/state/.codex/config.toml", existing)
		c.Chmod("/state/.codex/config.toml", 0o600)
	}
	return c
}

// codexOwn is a config.toml as Codex and its user leave it: comments, other
// tables on both sides, a server of the user's own with a multi-line array and
// an inline table.
const codexOwn = `# Codex configuration
model = "gpt-test"   # my favourite

[projects."/workspace"]
trust_level = "trusted"

# my own server
[mcp_servers.local]
command = "tool"
args = [
  "--flag",   # a comment inside
  "[not-a-table]",
]
env = { A = "1" }

[tui]
theme = "dark"
`

func TestComposeTOML_TheFirstSyncAsksShowsOnlyBatonsTablesBacksUpAndKeepsEveryOtherByte(t *testing.T) {
	env := newEnv(t)
	c := tomlSetup(t, env, codexOwn)

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	for _, want := range []string{"/home/dev/.codex/config.toml", "+[mcp_servers.tracker]", "+TRACKER_TOKEN = \"${TRACKER_TOKEN}\""} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("transcript lacks %q:\n%s", want, res.Stdout)
		}
	}
	for _, leak := range []string{"t0ken", "gpt-test", "trust_level"} {
		if strings.Contains(res.Stdout, leak) {
			t.Errorf("transcript shows %q, which is not Baton's to show:\n%s", leak, res.Stdout)
		}
	}
	// Appended after everything, one blank line between; nothing else moved.
	if got, want := c.ReadFile("/state/.codex/config.toml"), codexOwn+"\n"+trackerTable("t0ken"); got != want {
		t.Errorf("config.toml = %q, want %q", got, want)
	}
	if mode := c.Mode("/state/.codex/config.toml"); mode != 0o600 {
		t.Errorf("mode = %04o, want it kept", mode)
	}
	backup := onlyBackup(t, env, "app.codex")
	if got := env.ReadFile(filepath.Join(backup, "config.toml")); got != codexOwn {
		t.Errorf("backed up config.toml = %q, want the original", got)
	}
}

func TestComposeTOML_WithoutATerminalOrOnNoNothingIsWritten(t *testing.T) {
	env := newEnv(t)
	c := tomlSetup(t, env, codexOwn)

	noTerminal := env.RunWithStdin("y\n", "sync")
	no := env.RunTerminal([]string{"n"}, "sync")

	if noTerminal.ExitCode == 0 || no.ExitCode == 0 {
		t.Fatalf("exit codes = %d and %d, want both non-zero", noTerminal.ExitCode, no.ExitCode)
	}
	if !strings.Contains(noTerminal.Stderr, "/home/dev/.codex/config.toml") {
		t.Errorf("stderr = %q, want it to name the file", noTerminal.Stderr)
	}
	if got := c.ReadFile("/state/.codex/config.toml"); got != codexOwn {
		t.Errorf("config.toml = %q, want it untouched", got)
	}
	if c.Exists("/state/.codex/hooks.json") || env.Exists(".baton/manifests/app.codex.json") {
		t.Error("something else was written although nobody agreed")
	}
}

func TestComposeTOML_LaterSyncsUpdateTheirTableInPlaceWithoutAskingAndKeepTheRest(t *testing.T) {
	env := newEnv(t)
	c := tomlSetup(t, env, codexOwn)
	if res := env.RunTerminal([]string{"y"}, "sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: %s", res.Stdout)
	}
	// The user adds more after Baton's tables, with a comment between.
	later := codexOwn + "\n" + trackerTable("t0ken") + "\n# added later\n[features]\nfast = true\n"
	c.WriteFile("/state/.codex/config.toml", later)
	c.Chmod("/state/.codex/config.toml", 0o600)
	before := len(composeDockerCalls(env))

	unchanged := env.Run("sync")
	writeSecrets(t, env, 0o600, "TRACKER_TOKEN=rotated\n")
	rotated := env.Run("sync") // no terminal: it must not ask

	if unchanged.ExitCode != 0 || rotated.ExitCode != 0 {
		t.Fatalf("exit codes = %d and %d, stderr=%q %q", unchanged.ExitCode, rotated.ExitCode, unchanged.Stderr, rotated.Stderr)
	}
	want := codexOwn + "\n" + trackerTable("rotated") + "\n# added later\n[features]\nfast = true\n"
	if got := c.ReadFile("/state/.codex/config.toml"); got != want {
		t.Errorf("config.toml = %q, want %q", got, want)
	}
	if before == len(composeDockerCalls(env)) {
		t.Error("the second sync made no docker call")
	}
}

func TestComposeTOML_ASecondSyncWithoutChangesWritesNothing(t *testing.T) {
	env := newEnv(t)
	tomlSetup(t, env, codexOwn)
	if res := env.RunTerminal([]string{"y"}, "sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: %s", res.Stdout)
	}
	before := len(env.Calls("docker"))

	res := env.Run("sync")

	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "app.codex: 0 files written, 0 removed") {
		t.Errorf("exit code = %d, stdout=%q, want nothing written", res.ExitCode, res.Stdout)
	}
	for _, call := range env.Calls("docker")[before:] {
		if line := strings.Join(call.Args, " "); strings.Contains(line, "mktemp") {
			t.Errorf("docker call %q writes a file", line)
		}
	}
}

func TestComposeTOML_ATableSplitOverTheFileIsReplacedAtItsFirstPlaceAndItsSubTableGoes(t *testing.T) {
	env := newEnv(t)
	split := `[mcp_servers.tracker]
command = "old"

[other]
x = 1

# the env of the tracker, apart
[mcp_servers.tracker.env]
TRACKER_TOKEN = "pasted"

[tail]
y = 2
`
	c := tomlSetup(t, env, split)

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	if strings.Contains(res.Stdout, "pasted") {
		t.Errorf("transcript shows the pasted secret:\n%s", res.Stdout)
	}
	want := trackerTable("t0ken") + "\n[other]\nx = 1\n\n# the env of the tracker, apart\n[tail]\ny = 2\n"
	if got := c.ReadFile("/state/.codex/config.toml"); got != want {
		t.Errorf("config.toml = %q, want %q", got, want)
	}
}

func TestComposeTOML_ATableEditedByHandAsksAgainAndNeverShowsTheSecret(t *testing.T) {
	env := newEnv(t)
	c := tomlSetup(t, env, codexOwn)
	if res := env.RunTerminal([]string{"y"}, "sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: %s", res.Stdout)
	}
	edited := strings.Replace(c.ReadFile("/state/.codex/config.toml"), `TRACKER_TOKEN = "t0ken"`, `TRACKER_TOKEN = "hand-pasted-secret"`+"\nEXTRA = \"1\"", 1)
	c.WriteFile("/state/.codex/config.toml", edited)
	c.Chmod("/state/.codex/config.toml", 0o600)

	noTerminal := env.Run("sync")
	res := env.RunTerminal([]string{"y"}, "sync")

	if noTerminal.ExitCode == 0 {
		t.Error("sync went on without asking although the table was edited")
	}
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "${TRACKER_TOKEN}") {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	for _, leak := range []string{"hand-pasted-secret", "t0ken"} {
		if strings.Contains(res.Stdout+noTerminal.Stderr, leak) {
			t.Errorf("a question shows %q", leak)
		}
	}
	if got, want := c.ReadFile("/state/.codex/config.toml"), codexOwn+"\n"+trackerTable("t0ken"); got != want {
		t.Errorf("config.toml = %q, want Baton's table back", got)
	}
}

func TestComposeTOML_ATableCutOutByHandAsksAgain(t *testing.T) {
	env := newEnv(t)
	c := tomlSetup(t, env, codexOwn)
	if res := env.RunTerminal([]string{"y"}, "sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: %s", res.Stdout)
	}
	c.WriteFile("/state/.codex/config.toml", codexOwn)
	c.Chmod("/state/.codex/config.toml", 0o600)

	noTerminal := env.Run("sync")
	res := env.RunTerminal([]string{"y"}, "sync")

	if noTerminal.ExitCode == 0 || res.ExitCode != 0 {
		t.Fatalf("exit codes = %d and %d, want it to ask", noTerminal.ExitCode, res.ExitCode)
	}
	if got, want := c.ReadFile("/state/.codex/config.toml"), codexOwn+"\n"+trackerTable("t0ken"); got != want {
		t.Errorf("config.toml = %q, want the table back", got)
	}
}

func TestComposeTOML_RemovingAServerFromTheSourceTakesOutItsTableAndSubTablesOnly(t *testing.T) {
	env := newEnv(t)
	c := tomlSetup(t, env, codexOwn)
	if res := env.RunTerminal([]string{"y"}, "sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: %s", res.Stdout)
	}
	// Baton's table in the middle, with the user's tables on both sides.
	middle := strings.Replace(codexOwn, "[tui]", trackerTable("t0ken")+"\n[tui]", 1)
	c.WriteFile("/state/.codex/config.toml", middle)
	c.Chmod("/state/.codex/config.toml", 0o600)
	env.SourceFile("mcp.json", `{"mcpServers": {}}`)
	env.CommitSource("drop the server")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := c.ReadFile("/state/.codex/config.toml"); got != codexOwn {
		t.Errorf("config.toml = %q, want the user's file as it was", got)
	}
	if keys := manifestKeys(t, env, "app.codex"); len(keys) != 0 {
		t.Errorf("manifest keys = %v, want no server left", keys)
	}
}

func TestComposeTOML_ANewFileIsPrivateAndAnExistingOneThatOthersCanReadIsRefusedBecauseOfTheSecrets(t *testing.T) {
	env := newEnv(t)
	c := tomlSetup(t, env, "")

	created := env.Run("sync")

	if created.ExitCode != 0 || c.Mode("/state/.codex/config.toml") != 0o600 {
		t.Fatalf("exit code = %d, mode = %v, want it created private", created.ExitCode, c.Mode("/state/.codex/config.toml"))
	}
	if got := c.ReadFile("/state/.codex/config.toml"); got != trackerTable("t0ken") {
		t.Errorf("config.toml = %q, want just the table", got)
	}

	other := newEnv(t)
	c = tomlSetup(t, other, codexOwn)
	c.Chmod("/state/.codex/config.toml", 0o644)

	res := other.Run("sync")

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "accessible by others") || !strings.Contains(res.Stderr, "chmod 600") {
		t.Errorf("exit code = %d, stderr=%q, want the chmod advice", res.ExitCode, res.Stderr)
	}
	if got := c.ReadFile("/state/.codex/config.toml"); got != codexOwn {
		t.Error("the file was changed")
	}
}

func TestComposeTOML_AServerWrittenWithDottedKeysOrAnInlineTableIsRefusedNotGuessedAt(t *testing.T) {
	cases := map[string]string{
		"dotted keys at the top":    "mcp_servers.tracker.command = \"x\"\n",
		"dotted keys in the parent": "[mcp_servers]\ntracker.command = \"x\"\n",
		"an inline table":           "[mcp_servers]\ntracker = { command = \"x\" }\n",
		"the whole parent inline":   "mcp_servers = { tracker = { command = \"x\" } }\n",
		"an array of tables":        "[[mcp_servers.tracker]]\ncommand = \"x\"\n",
	}
	for name, existing := range cases {
		t.Run(name, func(t *testing.T) {
			env := newEnv(t)
			c := tomlSetup(t, env, existing)

			res := env.Run("sync")

			if res.ExitCode != 1 || !strings.Contains(res.Stderr, "mcp_servers.tracker is written as") {
				t.Errorf("exit code = %d, stderr=%q, want a refusal that names the table", res.ExitCode, res.Stderr)
			}
			if got := c.ReadFile("/state/.codex/config.toml"); got != existing {
				t.Errorf("config.toml = %q, want it untouched", got)
			}
		})
	}
}

func TestComposeTOML_AnInlineTableOfAnotherServerAndOneInsideBatonsTableAreFine(t *testing.T) {
	env := newEnv(t)
	existing := "[mcp_servers]\nother = { command = \"x\" }\n\n[mcp_servers.tracker]\ncommand = \"old\"\nenv = { A = \"1\" }\n"
	c := tomlSetup(t, env, existing)

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	want := "[mcp_servers]\nother = { command = \"x\" }\n\n" + trackerTable("t0ken")
	if got := c.ReadFile("/state/.codex/config.toml"); got != want {
		t.Errorf("config.toml = %q, want %q", got, want)
	}
}

func TestComposeTOML_CRLFLineEndingsAreKeptAndMixedOnesRefused(t *testing.T) {
	crlf := strings.ReplaceAll(codexOwn, "\n", "\r\n")
	env := newEnv(t)
	c := tomlSetup(t, env, crlf)

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	want := crlf + "\r\n" + strings.ReplaceAll(trackerTable("t0ken"), "\n", "\r\n")
	if got := c.ReadFile("/state/.codex/config.toml"); got != want {
		t.Errorf("config.toml = %q, want %q", got, want)
	}

	mixed := newEnv(t)
	c = tomlSetup(t, mixed, "a = 1\r\nb = 2\n")
	refused := mixed.Run("sync")
	if refused.ExitCode != 1 || !strings.Contains(refused.Stderr, "mixes CRLF and LF") {
		t.Errorf("exit code = %d, stderr=%q, want mixed line endings refused", refused.ExitCode, refused.Stderr)
	}
	if got := c.ReadFile("/state/.codex/config.toml"); got != "a = 1\r\nb = 2\n" {
		t.Error("the file was changed")
	}
}

func TestComposeTOML_AFileThatIsNotTOMLStopsTheProfileAndIsLeftAlone(t *testing.T) {
	env := newEnv(t)
	c := tomlSetup(t, env, "this is = not [valid\n")

	res := env.Run("sync")

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "app.codex") || !strings.Contains(res.Stderr, "config.toml") {
		t.Errorf("exit code = %d, stderr=%q, want the Profile and the file named", res.ExitCode, res.Stderr)
	}
	if got := c.ReadFile("/state/.codex/config.toml"); got != "this is = not [valid\n" {
		t.Error("the file was changed")
	}
}

func TestComposeTOML_StringsThatLookLikeHeadersOrQuotesDoNotConfuseTheEditor(t *testing.T) {
	env := newEnv(t)
	existing := `[notes]
text = """
[mcp_servers.tracker]
command = "decoy"
"""
quoted = 'a # not a comment [x]'
list = [ "]", '[', # [mcp_servers.tracker]
  [1, 2],
]

[mcp_servers."odd.name"]
command = "x"
`
	c := tomlSetup(t, env, existing)

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	if got, want := c.ReadFile("/state/.codex/config.toml"), existing+"\n"+trackerTable("t0ken"); got != want {
		t.Errorf("config.toml = %q, want %q", got, want)
	}
}

// codexMountedContainer is batonMountedContainer with an environment.
func codexMountedContainer(env *batontest.Env, repo string, running bool, environment ...string) *batontest.Container {
	env.WriteFile(".baton/bin/baton", "#!/bin/sh\n", true)
	return env.AddContainer(batontest.ContainerSpec{
		Service: "app", Running: running, User: "1001:1001", Home: "/home/dev", Env: environment,
		Binds:   map[string]string{repo: "/workspace", env.Path(".baton/projects/app"): "/baton/project"},
		BindsRO: map[string]string{env.Path(".baton/bin"): "/baton/bin"},
	})
}

func codexExecLine(repo, codexHome string) string {
	return "cd " + repo + " && docker compose -f " + filepath.Join(repo, "docker-compose.yml") +
		" exec -w /workspace app env -u ANTHROPIC_API_KEY -u OPENAI_API_KEY CODEX_HOME=" + codexHome + " codex"
}

func TestComposeCodexNew_ExecsCodexInTheServiceWithItsHomeAndWithoutAPIKeys(t *testing.T) {
	cases := []struct {
		name, extra string
		environment []string
		wantHome    string
	}{
		{"default", "", nil, "/home/dev/.codex"},
		{"from the service environment", "", []string{"CODEX_HOME=/state/codex-home"}, "/state/codex-home"},
		{"from the config", `dir = "~/codex-two"` + "\n", []string{"CODEX_HOME=/state/codex-home"}, "/home/dev/codex-two"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newEnv(t)
			env.WriteConfig(composeCodexConfig(env, tc.extra))
			repo := composeProject(t, env, "app")
			codexMountedContainer(env, repo, true, tc.environment...)

			res := env.Run("new", "app", "login-fix", "--agent", "codex")

			if res.ExitCode != 0 {
				t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
			}
			if got, want := typedLine(t, env), codexExecLine(repo, tc.wantHome); got != want {
				t.Errorf("line = %q, want %q", got, want)
			}
			if title := guakeArgs(env)[0][3]; title != "app/login-fix:codex" {
				t.Errorf("tab title = %q, want app/login-fix:codex", title)
			}
			if calls := env.Calls("codex"); len(calls) != 0 {
				t.Errorf("codex stub calls = %v, want none: Codex runs inside docker, which is a fake", calls)
			}
			for _, call := range composeDockerCalls(env) {
				if startsOrChangesAService(call) {
					t.Errorf("docker call %q starts or changes a service that was running", call)
				}
			}
		})
	}
}

func TestComposeCodexNew_AStoppedServiceIsBroughtUpAndThePromptComesLast(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(composeCodexConfig(env, ""))
	repo := composeProject(t, env, "app")
	codexMountedContainer(env, repo, false)

	res := env.Run("new", "app", "--agent", "codex")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	up := "compose -f " + filepath.Join(repo, "docker-compose.yml") + " up -d app"
	found := false
	for _, call := range composeDockerCalls(env) {
		found = found || call == up
	}
	if !found {
		t.Errorf("docker calls = %q, want %q", composeDockerCalls(env), up)
	}
}

func TestComposeCodexNew_SeveralCodexProfilesNeedAChoiceAndAClaudeProfileIsNotOne(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(composeCodexConfig(env, "") + `
[projects.app.compose.profiles.codex2]
agent = "codex"
dir   = "/state/codex2"

[projects.app.compose.profiles.claude]
agent = "claude"
dir   = "~/.claude"
`)
	repo := composeProject(t, env, "app")
	codexMountedContainer(env, repo, true)

	ambiguous := env.Run("new", "app", "--agent", "codex")
	chosen := env.Run("new", "app", "--agent", "codex", "--profile", "app.codex2")
	wrong := env.Run("new", "app", "--agent", "codex", "--profile", "app.claude")

	if ambiguous.ExitCode == 0 || !strings.Contains(ambiguous.Stderr, "several codex profiles in the compose service of project app (app.codex, app.codex2)") {
		t.Errorf("exit code = %d, stderr=%q, want the choice demanded", ambiguous.ExitCode, ambiguous.Stderr)
	}
	if chosen.ExitCode != 0 {
		t.Fatalf("chosen: exit code = %d, stderr=%q", chosen.ExitCode, chosen.Stderr)
	}
	if got, want := typedLine(t, env), codexExecLine(repo, "/state/codex2"); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
	if wrong.ExitCode == 0 || !strings.Contains(wrong.Stderr, `unknown profile "app.claude"`) {
		t.Errorf("exit code = %d, stderr=%q, want a Claude Profile refused for codex", wrong.ExitCode, wrong.Stderr)
	}
}

func TestComposeCodexNew_AProjectWithOnlyAClaudeProfileHasNoCodexProfile(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(composeConfig(env, ""))
	repo := composeProject(t, env, "app")
	batonMountedContainer(env, repo, true)

	res := env.Run("new", "app", "--agent", "codex")

	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "has no codex profile in its compose service") {
		t.Errorf("exit code = %d, stderr=%q, want it to say there is no codex profile", res.ExitCode, res.Stderr)
	}
	if len(env.Calls("guake")) != 0 {
		t.Error("a tab was opened")
	}
}

// composeCodexHook plays Codex in the service calling its hook.
func composeCodexHook(t *testing.T, env *batontest.Env, root, event, sessionID, cwd string) {
	t.Helper()
	res := env.RunWithEnv([]string{"BATON_CONTAINER_ROOT=" + root}, fixture(t, "codex", event, sessionID, cwd),
		"hook", "codex", "--profile", "app.codex", "--runtime", "compose")
	if res.ExitCode != 0 || res.Stdout != "" || res.Stderr != "" {
		t.Fatalf("hook %s: exit code = %d, stdout=%q, stderr=%q, want a silent success", event, res.ExitCode, res.Stdout, res.Stderr)
	}
}

func TestComposeCodexLs_ALaunchThatReportsNothingIsFlaggedWithTheHooksHintUntilTheFirstEvent(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(composeCodexConfig(env, ""))
	repo := composeProject(t, env, "app")
	codexMountedContainer(env, repo, true)
	if res := env.Run("new", "app", "--agent", "codex"); res.ExitCode != 0 {
		t.Fatalf("new: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	silent := env.Run("ls")

	if rows := lsRows(t, env); !reflect.DeepEqual(rows, [][]string{{"app", "YYYY-MM-DD-1", "codex", "-", "app.codex", "compose", "no_events"}}) {
		t.Errorf("rows = %v, want the launch with no events", rows)
	}
	if hints := hintLines(silent.Stdout); len(hints) != 1 || stable(hints[0]) != hooksHint {
		t.Errorf("hints = %q, want exactly %q", hints, hooksHint)
	}
	// The marker is under the Project's directory, which the container mounts.
	var marker map[string]any
	if err := json.Unmarshal([]byte(env.ReadFile(".baton/projects/app/launches/codex.json")), &marker); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{"agent": "codex", "profile": "app.codex", "runtime": "compose", "title": "app/" + marker["task"].(string) + ":codex"} {
		if marker[key] != want {
			t.Errorf("%s = %#v, want %#v", key, marker[key], want)
		}
	}

	composeCodexHook(t, env, containerRoot(t, env, "app"), "SessionStart", "thr_1", "/workspace")

	heard := env.Run("ls")
	if hints := hintLines(heard.Stdout); len(hints) != 0 {
		t.Errorf("hints = %q, want none once an Event arrived", hints)
	}
	if rows := lsRows(t, env); !reflect.DeepEqual(rows, [][]string{{"app", "YYYY-MM-DD-1", "codex", "thr_1", "app.codex", "compose", "waiting_input"}}) {
		t.Errorf("rows = %v, want the Session with its status", rows)
	}
}

// composeCodexEvent appends an Event of a Codex Session of Project app in its
// Compose container, as the hook there would have written it.
func composeCodexEvent(t *testing.T, env *batontest.Env, sessionID, ts, event string, p proc) {
	t.Helper()
	doc := map[string]any{
		"v": 1, "ts": ts, "agent": "codex", "profile": "app.codex", "runtime": "compose",
		"host": p.host, "agent_pid": p.pid, "agent_start": p.start, "session_id": sessionID,
		"event": event, "cwd": "/workspace", "payload": map[string]any{},
	}
	line, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := ".baton/projects/app/sessions/codex-" + sessionID + ".jsonl"
	existing := ""
	if env.Exists(path) {
		existing = env.ReadFile(path)
	}
	env.WriteFile(path, existing+string(line)+"\n")
}

func TestComposeCodexLost_ACodexSessionIsLostWhenItsAgentIsGoneFromTheRunningContainerAndWatchSeesItLikeClaudes(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(composeCodexConfig(env, ""))
	repo := composeProject(t, env, "app")
	c := codexMountedContainer(env, repo, true)
	c.Process(100, "codex", 5000)
	composeCodexEvent(t, env, "thr_1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: aaaa, pid: 100, start: 5000})
	composeCodexEvent(t, env, "thr_2", "2031-03-04T10:01:00Z", "PreToolUse", proc{host: aaaa, pid: 200, start: 6000})

	rows := lsRows(t, env) // the most recent first

	if want := []string{"lost", "running"}; !reflect.DeepEqual(statuses(rows), want) {
		t.Errorf("statuses = %v, want %v: thr_2's process runs nothing", statuses(rows), want)
	}
	if rows[0][2] != "codex" || rows[0][5] != "compose" {
		t.Errorf("row = %v, want a compose Codex Session", rows[0])
	}
}

func TestComposeCodexHandoff_FromAClaudeSessionOnTheHostToCodexInTheServiceNamesTheHandoffByItsContainerPath(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(composeCodexConfig(env, ""))
	repo := composeProject(t, env, "app")
	c := codexMountedContainer(env, repo, true)
	c.MkdirAll("/workspace")
	commitFile(t, env, repo, "README.md", "hello\n", "first commit")
	hostEvent(t, env, "h1", "2031-03-04T10:00:00Z", "SessionStart", repo, proc{})
	hostEvent(t, env, "h1", "2031-03-04T10:05:00Z", "SessionEnd", repo, proc{})
	task := appTask(t, env)

	res := env.Run("handoff", task, "--to", "codex")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	line := typedLine(t, env)
	if !strings.Contains(line, " exec -w /workspace app env -u ANTHROPIC_API_KEY -u OPENAI_API_KEY CODEX_HOME=/home/dev/.codex codex 'continue the Task from Handoff /baton/project/handoffs/") {
		t.Errorf("line = %q, want Codex in the service, told about the Handoff by its container path", line)
	}
	if !env.Exists(".baton/projects/app/launches/codex.json") {
		t.Error("the receiver's launch was not recorded, so a silent Codex would go unnoticed")
	}
}

func TestComposeCodexConfig_ACodexProfileMayLeaveItsDirectoryOutButAClaudeOneMayNot(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env) + "\n[projects.x.compose]\nfiles = [\"c.yml\"]\nservice = \"s\"\n[projects.x.compose.profiles.c]\nagent = \"claude\"\n")

	res := env.Run("sync")

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, `profile x.c: dir "" must be an absolute path in the container`) {
		t.Errorf("exit code = %d, stderr=%q, want the Claude Profile without dir refused", res.ExitCode, res.Stderr)
	}
}
