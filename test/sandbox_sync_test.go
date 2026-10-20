package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/loadkpi/baton/internal/batontest"
)

// sandboxProject creates a git repository at ~/work/<name> that has an
// ai-sandbox (.ai-sandbox/run.sh and the home directory run.sh makes), and
// registers it as a Project with the sandbox Runtime. It returns the path of
// the sandbox's home directory, which is the HOME of the Agents inside.
func sandboxProject(t *testing.T, env *batontest.Env, name string) (repo, home string) {
	t.Helper()
	repo = newRepo(t, env, name)
	env.WriteFile(filepath.Join(repo, ".ai-sandbox", "run.sh"), "#!/bin/sh\n", true)
	home = filepath.Join(repo, ".ai-sandbox", "home")
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	env.WriteFile(".baton/projects/"+name+"/project.toml", "path = \""+repo+"\"\nruntime = \"sandbox\"\n")
	return repo, home
}

// sandboxHookCommand is the command sync writes into a Profile inside a
// sandbox: the binary where the container mounts it, guarded so that a
// container without it (an ai-sandbox started without Baton) stays silent.
// Claude runs the string through a shell. Whether Codex does is not in its
// documentation, so Codex gets the shell explicitly.
func sandboxHookCommand(agentName, profile string) string {
	guarded := "[ -x /baton/bin/baton ] || exit 0; exec /baton/bin/baton hook " + agentName + " --profile " + profile + " --runtime sandbox"
	if agentName == "codex" {
		return "sh -c '" + guarded + "'"
	}
	return guarded
}

func TestSandboxSync_ClaudeProfileInsideTheSandboxGetsSkillsAndStatusHooks(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	env.SourceFile("skills/greet/SKILL.md", "---\nname: greet\ndescription: Say hello\n---\nHello.\n")
	env.CommitSource("add skill")
	_, home := sandboxProject(t, env, "app")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got, want := env.ReadFile(filepath.Join(home, ".claude", "skills", "greet", "SKILL.md")), "---\nname: greet\ndescription: Say hello\n---\nHello.\n"; got != want {
		t.Errorf("skill = %q, want %q", got, want)
	}
	want := make(map[string][]string)
	for _, event := range statusEvents {
		want[event] = []string{sandboxHookCommand("claude", "app.claude")}
	}
	if got := hookCommands(t, env, filepath.Join(home, ".claude", "settings.json")); !reflect.DeepEqual(got, want) {
		t.Errorf("hooks = %v, want %v", got, want)
	}
	if !strings.Contains(res.Stdout, "app.claude: ") {
		t.Errorf("stdout = %q, want a line for the Profile app.claude", res.Stdout)
	}
}

// sandboxConfig is the standard config plus the tags the sandbox of Project
// "app" accepts.
func sandboxConfig(env *batontest.Env, tags string) string {
	return standardConfig(env) + "\n[projects.app]\ntags = [" + tags + "]\n"
}

func TestSandboxSync_CodexGetsTheSkillsWhereItReadsThemWithoutBatonsTagsKey(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(sandboxConfig(env, `"work"`))
	env.SourceFile("skills/review/SKILL.md", "---\nname: review\ntags: [work]\ndescription: Review code\n---\nReview.\n")
	env.SourceFile("skills/review/scripts/lint.sh", "#!/bin/sh\necho lint\n", true)
	env.CommitSource("add skill")
	_, home := sandboxProject(t, env, "app")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	dir := filepath.Join(home, ".agents", "skills", "review")
	if got, want := env.ReadFile(filepath.Join(dir, "SKILL.md")), "---\nname: review\ndescription: Review code\n---\nReview.\n"; got != want {
		t.Errorf("Codex skill = %q, want %q", got, want)
	}
	if got, want := env.ReadFile(filepath.Join(dir, "scripts", "lint.sh")), "#!/bin/sh\necho lint\n"; got != want {
		t.Errorf("Codex script = %q, want %q", got, want)
	}
	if env.Mode(filepath.Join(dir, "scripts", "lint.sh"))&0o100 == 0 {
		t.Error("the script lost its execute bit")
	}
	if got, want := env.ReadFile(filepath.Join(home, ".claude", "skills", "review", "SKILL.md")), "---\nname: review\ntags: [work]\ndescription: Review code\n---\nReview.\n"; got != want {
		t.Errorf("Claude skill = %q, want it verbatim, tags included", got)
	}
}

// codexStatusEvents are the Codex Events of the status table, sorted.
var codexStatusEvents = []string{
	"Interrupt", "PermissionRequest", "PostToolUse", "PreToolUse",
	"SessionEnd", "SessionStart", "Stop", "UserPromptSubmit",
}

func TestSandboxSync_CodexGetsStatusHooksInItsHooksJSON(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	_, home := sandboxProject(t, env, "app")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := make(map[string][]string)
	for _, event := range codexStatusEvents {
		want[event] = []string{sandboxHookCommand("codex", "app.codex")}
	}
	if got := hookCommands(t, env, filepath.Join(home, ".codex", "hooks.json")); !reflect.DeepEqual(got, want) {
		t.Errorf("hooks = %v, want %v", got, want)
	}
	if !strings.Contains(res.Stdout, "app.codex: status hooks installed") || !strings.Contains(res.Stdout, "/hooks") {
		t.Errorf("stdout = %q, want the note that Codex asks to trust the hooks with /hooks", res.Stdout)
	}
}

func TestSandboxSync_CodexGetsInstructionsAndMCPServersOnlyAsFragments(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(sandboxConfig(env, `"work"`))
	env.SourceFile("AGENTS.md", "Be brief.\n\n<!-- baton:tags work -->\nUse the tracker.\n<!-- /baton:tags -->\n\n<!-- baton:tags personal -->\nHome rules.\n<!-- /baton:tags -->\n")
	env.SourceFile("mcp.json", `{"mcpServers": {
  "tracker": {"tags": ["work"], "type": "http", "url": "https://tracker.example/mcp", "headers": {"Authorization": "Bearer ${TRACKER_TOKEN}"}},
  "files": {"command": "npx", "args": ["-y", "files-server"], "env": {"ROOT": "/workspace"}},
  "events": {"type": "sse", "url": "https://events.example/sse"},
  "private": {"tags": ["personal"], "command": "private-server"}
}}`)
	env.CommitSource("add instructions and servers")
	writeSecrets(t, env, 0o600, "TRACKER_TOKEN=tok-123\n")
	_, home := sandboxProject(t, env, "app")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	codex := filepath.Join(home, ".codex")
	if got, want := env.ReadFile(filepath.Join(codex, "AGENTS.d", "baton.md")), "Be brief.\n\nUse the tracker.\n"; got != want {
		t.Errorf("instructions fragment = %q, want %q", got, want)
	}
	wantTOML := "# Written by baton sync. Do not edit.\n" +
		"[mcp_servers.files]\n" +
		"args = [\"-y\", \"files-server\"]\n" +
		"command = \"npx\"\n" +
		"[mcp_servers.files.env]\n" +
		"ROOT = \"/workspace\"\n" +
		"[mcp_servers.tracker]\n" +
		"url = \"https://tracker.example/mcp\"\n" +
		"[mcp_servers.tracker.http_headers]\n" +
		"Authorization = \"Bearer tok-123\"\n"
	if got := env.ReadFile(filepath.Join(codex, "config.d", "baton.toml")); got != wantTOML {
		t.Errorf("MCP fragment = %q, want %q", got, wantTOML)
	}
	if mode := env.Mode(filepath.Join(codex, "config.d", "baton.toml")); mode != 0o600 {
		t.Errorf("MCP fragment mode = %04o, want 0600: it holds a secret", mode)
	}
	for _, rel := range []string{"AGENTS.md", "config.toml"} {
		if env.Exists(filepath.Join(codex, rel)) {
			t.Errorf("sync wrote %s, which ai-sandbox generates", rel)
		}
	}
	if !strings.Contains(res.Stderr, `server "events"`) || !strings.Contains(res.Stderr, "sse") {
		t.Errorf("stderr = %q, want a warning that Codex has no SSE servers", res.Stderr)
	}
	if strings.Contains(res.Stdout+res.Stderr, "tok-123") {
		t.Error("a secret was printed")
	}
}

func TestSandboxSync_ClaudeInTheSandboxGetsInstructionsAndMCPServersLikeAHostProfile(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(sandboxConfig(env, `"work"`))
	env.SourceFile("AGENTS.md", "Be brief.\n\n<!-- baton:tags personal -->\nHome rules.\n<!-- /baton:tags -->\n")
	env.SourceFile("mcp.json", `{"mcpServers": {"tracker": {"tags": ["work"], "type": "http", "url": "https://tracker.example/mcp", "headers": {"Authorization": "Bearer ${TRACKER_TOKEN}"}}}}`)
	env.CommitSource("add instructions and servers")
	writeSecrets(t, env, 0o600, "TRACKER_TOKEN=tok-123\n")
	_, home := sandboxProject(t, env, "app")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got, want := env.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md")), "<!-- baton:begin -->\nBe brief.\n<!-- baton:end -->\n"; got != want {
		t.Errorf("CLAUDE.md = %q, want %q", got, want)
	}
	// Inside the container HOME is the sandbox home, so the default Profile
	// rule applies: Claude keeps its MCP servers in $HOME/.claude.json.
	want := `{
  "mcpServers": {
    "tracker": {
      "headers": {
        "Authorization": "Bearer tok-123"
      },
      "type": "http",
      "url": "https://tracker.example/mcp"
    }
  }
}
`
	if got := env.ReadFile(filepath.Join(home, ".claude.json")); got != want {
		t.Errorf(".claude.json = %q, want %q", got, want)
	}
	if mode := env.Mode(filepath.Join(home, ".claude.json")); mode != 0o600 {
		t.Errorf(".claude.json mode = %04o, want 0600", mode)
	}
	if env.Exists(filepath.Join(home, ".claude", ".claude.json")) {
		t.Error("the servers went to .claude/.claude.json, where Claude does not read them")
	}
}

// installAiSandbox puts a copy of ai-sandbox's launcher (testdata/ai-sandbox)
// into the Project, the way a user installs the template.
func installAiSandbox(t *testing.T, env *batontest.Env, repo string) {
	t.Helper()
	for _, name := range []string{"run.sh", "versions.env"} {
		data, err := os.ReadFile(filepath.Join("testdata", "ai-sandbox", ".ai-sandbox", name))
		if err != nil {
			t.Fatal(err)
		}
		env.WriteFile(filepath.Join(repo, ".ai-sandbox", name), string(data), name == "run.sh")
	}
}

// runLauncher runs the copy of ai-sandbox's run.sh of the Project in its
// print-args mode: it renders its configuration files and prints the docker
// invocation instead of running it.
func runLauncher(t *testing.T, env *batontest.Env, repo string) batontest.Result {
	t.Helper()
	res := env.Command("env", "AI_SANDBOX_PRINT_ARGS=1", "bash", filepath.Join(repo, ".ai-sandbox", "run.sh"), "--", "codex")
	if res.ExitCode != 0 {
		t.Fatalf("run.sh: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	return res
}

// readTree returns the content of every file below dir, by path relative to it.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSandboxSync_AndTheLauncherLeaveEachOthersFilesAlone(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	env.SourceFile("AGENTS.md", "Be brief.\n")
	env.SourceFile("skills/greet/SKILL.md", "---\nname: greet\ndescription: Say hello\n---\nHello.\n")
	env.SourceFile("mcp.json", `{"mcpServers": {"files": {"command": "npx"}}}`)
	env.CommitSource("add content")
	repo, home := sandboxProject(t, env, "app")
	installAiSandbox(t, env, repo)
	codex := filepath.Join(home, ".codex")

	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	synced := map[string]map[string]string{
		"codex dir":    readTree(t, codex),
		"agents skill": readTree(t, filepath.Join(home, ".agents")),
		"claude dir":   readTree(t, filepath.Join(home, ".claude")),
	}
	launcher := runLauncher(t, env, repo)

	// The launcher renders the Codex configuration and instructions, and
	// nothing else of the Profile: not the hooks, not the skills, not the
	// fragments. This is what the finding in DESIGN.md rests on.
	for _, rel := range []string{"config.toml", "AGENTS.md"} {
		if !env.Exists(filepath.Join(codex, rel)) {
			t.Errorf("the launcher did not render %s", rel)
		}
	}
	after := readTree(t, codex)
	for rel, content := range synced["codex dir"] {
		if after[rel] != content {
			t.Errorf("the launcher changed %s: %q, was %q", rel, after[rel], content)
		}
	}
	if !reflect.DeepEqual(readTree(t, filepath.Join(home, ".agents")), synced["agents skill"]) ||
		!reflect.DeepEqual(readTree(t, filepath.Join(home, ".claude")), synced["claude dir"]) {
		t.Error("the launcher changed synced skills or the Claude Profile")
	}
	if !strings.Contains(launcher.Stdout, "-v\n"+filepath.Join(home)+":/home/sandbox:rw\n") {
		t.Errorf("print-args output = %q, want the mount of the home directory", launcher.Stdout)
	}

	// A second sync leaves what the launcher rendered, and the launcher's
	// project-level settings, as they are, and finds nothing to write.
	generated := map[string]string{
		"config.toml": env.ReadFile(filepath.Join(codex, "config.toml")),
		"AGENTS.md":   env.ReadFile(filepath.Join(codex, "AGENTS.md")),
	}
	local := env.ReadFile(filepath.Join(repo, ".claude", "settings.local.json"))
	res := env.Run("sync")
	if res.ExitCode != 0 {
		t.Fatalf("second sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	for rel, content := range generated {
		if got := env.ReadFile(filepath.Join(codex, rel)); got != content {
			t.Errorf("sync changed %s, which the launcher owns", rel)
		}
	}
	if got := env.ReadFile(filepath.Join(repo, ".claude", "settings.local.json")); got != local {
		t.Error("sync changed .claude/settings.local.json, which the launcher owns")
	}
	if !strings.Contains(res.Stdout, "app.codex: 0 files written, 0 removed") {
		t.Errorf("stdout = %q, want nothing to write for app.codex", res.Stdout)
	}
}

// userHooks is a hooks.json of the user's own, with a hook on an Event Baton
// uses too.
const userHooks = `{
  "hooks": {
    "Stop": [
      {"hooks": [{"type": "command", "command": "notify-me"}]}
    ]
  }
}
`

func TestSandboxSync_AHooksFileOfTheUserIsAQuestionAndABackupAndWithoutTerminalAnError(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	_, home := sandboxProject(t, env, "app")
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	env.WriteFile(hooksPath, userHooks)

	res := env.RunWithStdin("y\n", "sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, hooksPath) || !strings.Contains(res.Stderr, "terminal") {
		t.Errorf("stderr = %q, want it to name %s and say there is no terminal", res.Stderr, hooksPath)
	}
	if got := env.ReadFile(hooksPath); got != userHooks {
		t.Errorf("hooks.json = %q, want it unchanged", got)
	}
	if env.Exists(filepath.Join(home, ".claude", "settings.json")) {
		t.Error("sync wrote another Profile although it stopped")
	}

	res = env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	if got := env.ReadFile(filepath.Join(onlyBackup(t, env, "app.codex"), "hooks.json")); got != userHooks {
		t.Errorf("backed up hooks.json = %q, want the original", got)
	}
	got := hookCommands(t, env, hooksPath)
	if want := []string{"notify-me", sandboxHookCommand("codex", "app.codex")}; !reflect.DeepEqual(got["Stop"], want) {
		t.Errorf("Stop hooks = %v, want %v", got["Stop"], want)
	}
}

func TestSandboxSync_ASkillOfTheUserInCodexsSkillsDirectoryIsBackedUpBeforeItIsReplaced(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	env.SourceFile("skills/greet/SKILL.md", "---\nname: greet\ndescription: Say hello\n---\nHello.\n")
	env.CommitSource("add skill")
	_, home := sandboxProject(t, env, "app")
	foreign := filepath.Join(home, ".agents", "skills", "greet")
	env.WriteFile(filepath.Join(foreign, "SKILL.md"), "Hand-made greeting.\n")

	res := env.RunWithStdin("", "sync")

	if res.ExitCode == 0 || !strings.Contains(res.Stderr, foreign) {
		t.Fatalf("exit code = %d, stderr=%q, want an error naming %s", res.ExitCode, res.Stderr, foreign)
	}
	if got := env.ReadFile(filepath.Join(foreign, "SKILL.md")); got != "Hand-made greeting.\n" {
		t.Errorf("foreign skill = %q, want it untouched", got)
	}

	res = env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	backup := onlyBackup(t, env, "app.codex")
	if got := env.ReadFile(filepath.Join(backup, ".agents", "skills", "greet", "SKILL.md")); got != "Hand-made greeting.\n" {
		t.Errorf("backed up skill = %q, want the original, inside the backup directory", got)
	}
	if got := env.ReadFile(filepath.Join(foreign, "SKILL.md")); !strings.Contains(got, "Hello.") {
		t.Errorf("skill = %q, want the one from the source", got)
	}
}

func TestSandboxSync_ARemovedSourceItemDisappearsFromTheSandboxProfilesAndNothingElseDoes(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	env.SourceFile("skills/greet/SKILL.md", "---\nname: greet\ndescription: Say hello\n---\nHello.\n")
	env.SourceFile("AGENTS.md", "Be brief.\n")
	env.SourceFile("mcp.json", `{"mcpServers": {"files": {"command": "npx"}}}`)
	env.CommitSource("add content")
	_, home := sandboxProject(t, env, "app")
	env.WriteFile(filepath.Join(home, ".agents", "skills", "mine", "SKILL.md"), "My own.\n")
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	env.SourceFile("mcp.json", `{"mcpServers": {}}`)
	env.CommitSource("drop the server")
	if err := os.RemoveAll(filepath.Join(env.SourceDir, "skills")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(env.SourceDir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	codex := filepath.Join(home, ".codex")
	for _, p := range []string{
		filepath.Join(home, ".agents", "skills", "greet"),
		filepath.Join(home, ".claude", "skills", "greet"),
		filepath.Join(codex, "AGENTS.d", "baton.md"),
		filepath.Join(codex, "config.d", "baton.toml"),
	} {
		if env.Exists(p) {
			t.Errorf("%s is still there", p)
		}
	}
	if got := env.ReadFile(filepath.Join(home, ".agents", "skills", "mine", "SKILL.md")); got != "My own.\n" {
		t.Errorf("the user's skill = %q, want it untouched", got)
	}
}

func TestSandboxSync_AFileThatGitTracksInTheSandboxHomeIsNeverChanged(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	repo, home := sandboxProject(t, env, "app")
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	env.WriteFile(hooksPath, userHooks)
	if res := env.Command("git", "-C", repo, "add", "--force", ".ai-sandbox/home/.codex/hooks.json"); res.ExitCode != 0 {
		t.Fatalf("git add: %s", res.Stderr)
	}

	res := env.Run("sync")

	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "git tracks it") {
		t.Fatalf("exit code = %d, stderr=%q, want a refusal because git tracks hooks.json", res.ExitCode, res.Stderr)
	}
	if got := env.ReadFile(hooksPath); got != userHooks {
		t.Errorf("hooks.json = %q, want it unchanged", got)
	}
}

func TestSandboxSync_WithoutTagsInTheConfigASandboxReceivesOnlyUntaggedItemsAndNoOtherSecrets(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	env.SourceFile("skills/greet/SKILL.md", "---\nname: greet\ndescription: Say hello\n---\nHello.\n")
	env.SourceFile("skills/review/SKILL.md", "---\nname: review\ntags: [work]\ndescription: Review\n---\nReview.\n")
	env.SourceFile("AGENTS.md", "Be brief.\n\n<!-- baton:tags work -->\nUse the tracker.\n<!-- /baton:tags -->\n")
	env.SourceFile("mcp.json", `{"mcpServers": {"tracker": {"tags": ["work"], "url": "https://tracker.example/mcp", "headers": {"Authorization": "Bearer ${TRACKER_TOKEN}"}}}}`)
	env.CommitSource("add content")
	_, home := sandboxProject(t, env, "app")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q (the secret of a server the sandbox does not accept must not be needed)", res.ExitCode, res.Stderr)
	}
	for _, p := range []string{
		filepath.Join(home, ".claude", "skills", "review"),
		filepath.Join(home, ".agents", "skills", "review"),
		filepath.Join(home, ".codex", "config.d"),
		filepath.Join(home, ".claude.json"),
	} {
		if env.Exists(p) {
			t.Errorf("%s exists, but the sandbox accepts no tags", p)
		}
	}
	if got := env.ReadFile(filepath.Join(home, ".codex", "AGENTS.d", "baton.md")); got != "Be brief.\n" {
		t.Errorf("instructions fragment = %q, want only the untagged part", got)
	}
	if !env.Exists(filepath.Join(home, ".agents", "skills", "greet", "SKILL.md")) {
		t.Error("the untagged skill is missing")
	}
}

func TestSandboxSync_AManifestPerSandboxProfileRecordsEveryOwnedFileAndHook(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	env.SourceFile("skills/greet/SKILL.md", "---\nname: greet\ndescription: Say hello\n---\nHello.\n")
	env.SourceFile("AGENTS.md", "Be brief.\n")
	env.SourceFile("mcp.json", `{"mcpServers": {"files": {"command": "npx"}}}`)
	env.CommitSource("add content")
	sandboxProject(t, env, "app")

	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	var manifest struct {
		Profile string `json:"profile"`
		Files   []struct {
			Path string `json:"path"`
		} `json:"files"`
		Keys []struct {
			Path string   `json:"path"`
			Key  []string `json:"key"`
		} `json:"keys"`
	}
	if err := json.Unmarshal([]byte(env.ReadFile(".baton/manifests/app.codex.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, f := range manifest.Files {
		files = append(files, f.Path)
	}
	want := []string{"../.agents/skills/greet/SKILL.md", "AGENTS.d/baton.md", "config.d/baton.toml"}
	if manifest.Profile != "app.codex" || !reflect.DeepEqual(files, want) {
		t.Errorf("manifest = %+v, want Profile app.codex with files %v", manifest, want)
	}
	if len(manifest.Keys) != len(codexStatusEvents) || manifest.Keys[0].Path != "hooks.json" {
		t.Errorf("manifest keys = %+v, want one hook per Event in hooks.json", manifest.Keys)
	}
}

func TestSandboxSync_OnlyRegisteredProjectsWithTheSandboxRuntimeAndAHomeAreTargets(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env) + "\n[projects.ghost]\ntags = [\"work\"]\n")
	plain := newRepo(t, env, "plain")
	env.WriteFile(".baton/projects/plain/project.toml", "path = \""+plain+"\"\nruntime = \"host\"\n")
	fresh := newRepo(t, env, "fresh")
	env.WriteFile(filepath.Join(fresh, ".ai-sandbox", "run.sh"), "#!/bin/sh\n", true)
	env.WriteFile(".baton/projects/fresh/project.toml", "path = \""+fresh+"\"\nruntime = \"sandbox\"\n")
	env.WriteFile(filepath.Join(env.Path("work"), "unregistered", ".ai-sandbox", "home", ".codex", "keep"), "x")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	for _, p := range []string{filepath.Join(plain, ".claude"), filepath.Join(fresh, ".ai-sandbox", "home"), filepath.Join(env.Path("work"), "unregistered", ".ai-sandbox", "home", ".codex", "hooks.json")} {
		if env.Exists(p) {
			t.Errorf("sync created %s", p)
		}
	}
	for _, want := range []string{"project fresh: skipped", "run.sh", "[projects.ghost]"} {
		if !strings.Contains(res.Stderr, want) {
			t.Errorf("stderr = %q, want it to mention %q", res.Stderr, want)
		}
	}
	if strings.Contains(res.Stdout, "plain.") || strings.Contains(res.Stdout, "fresh.") {
		t.Errorf("stdout = %q, want no Profile line for them", res.Stdout)
	}
}

func TestSandboxSync_AHostProfileCalledLikeASandboxProfileIsRefused(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.MakeProfileDir(".claude-app")
	env.WriteConfig(standardConfig(env) + "\n[profiles.\"app.claude\"]\nagent = \"claude\"\ndir = \"" + env.Path(".claude-app") + "\"\n")
	sandboxProject(t, env, "app")

	res := env.Run("sync")

	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "app.claude") {
		t.Errorf("exit code = %d, stderr=%q, want an error naming app.claude", res.ExitCode, res.Stderr)
	}
	if env.Exists(".baton/manifests") {
		t.Error("sync wrote manifests although it refused")
	}
}

func TestSandboxSync_HostProfilesAreClaudeOnly(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".codex")
	env.WriteConfig(standardConfig(env) + "\n[profiles.codex]\nagent = \"codex\"\ndir = \"" + env.Path(".codex") + "\"\n")

	res := env.Run("sync")

	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "profile codex") || !strings.Contains(res.Stderr, `"codex"`) {
		t.Errorf("exit code = %d, stderr=%q, want the config refused for a Codex host Profile", res.ExitCode, res.Stderr)
	}
}

func TestSandboxSync_CodexCopiesLoseTheTagsKeyInEveryFormAndNothingElse(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(sandboxConfig(env, `"work"`))
	env.SourceFile("skills/block/SKILL.md", "---\nname: block\ntags:\n  - work # sandbox\n\n  - personal\ndescription: Block list\n---\nBody mentions tags: work.\n")
	env.SourceFile("skills/scalar/SKILL.md", "---\nname: scalar\ndescription: Scalar\ntags: work\n---\nBody.\n")
	env.SourceFile("skills/plain/SKILL.md", "---\nname: plain\ndescription: No tags\n---\nBody.\n")
	env.SourceFile("skills/bare/SKILL.md", "No front matter, tags: work\n")
	env.CommitSource("add skills")
	_, home := sandboxProject(t, env, "app")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := map[string]string{
		"block":  "---\nname: block\ndescription: Block list\n---\nBody mentions tags: work.\n",
		"scalar": "---\nname: scalar\ndescription: Scalar\n---\nBody.\n",
		"plain":  "---\nname: plain\ndescription: No tags\n---\nBody.\n",
		"bare":   "No front matter, tags: work\n",
	}
	for name, content := range want {
		if got := env.ReadFile(filepath.Join(home, ".agents", "skills", name, "SKILL.md")); got != content {
			t.Errorf("Codex skill %s = %q, want %q", name, got, content)
		}
	}
}
