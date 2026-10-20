package test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/loadkpi/baton/internal/batontest"
)

// newConfig is a config with one Claude Profile under a relocated directory.
func newConfig(env *batontest.Env, extra string) string {
	return standardConfig(env) + extra
}

func TestNew_OpensAGuakeTabInTheProjectsDirectoryWithItsTitleAndTheAgentCommand(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")

	res := env.Run("new", repo, "--agent", "claude")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	got := guakeArgs(env)
	want := [][]string{
		{"-e", "cd " + repo + " && env -u ANTHROPIC_API_KEY -u OPENAI_API_KEY CLAUDE_CONFIG_DIR=" + env.Path(".claude-personal") + " claude", "-r", "my-app/YYYY-MM-DD-1:claude"},
	}
	for _, call := range got {
		call[3] = stable(call[3]) // the unnamed Task is named after today
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("guake calls = %q, want %q", got, want)
	}
}

// guakeTitle returns the title of the tab of the only guake call.
func guakeTitle(t *testing.T, env *batontest.Env) string {
	t.Helper()
	typedLine(t, env)
	return guakeArgs(env)[0][3]
}

// guakeArgs returns the arguments of every recorded guake call.
func guakeArgs(env *batontest.Env) [][]string {
	var out [][]string
	for _, c := range env.Calls("guake") {
		out = append(out, c.Args)
	}
	return out
}

func TestNew_RegistersTheProjectAndLaterRunsCanUseItsName(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")

	first := env.Run("new", repo, "--agent", "claude")
	second := env.Run("new", "my-app", "--agent", "claude")

	if first.ExitCode != 0 || second.ExitCode != 0 {
		t.Fatalf("exit codes = %d, %d; stderr=%q, %q", first.ExitCode, second.ExitCode, first.Stderr, second.Stderr)
	}
	if got, want := env.ReadFile(".baton/projects/my-app/project.toml"), "path = \""+repo+"\"\nruntime = \"host\"\n"; got != want {
		t.Errorf("project.toml = %q, want %q", got, want)
	}
	calls := guakeArgs(env)
	if len(calls) != 2 || calls[0][1] != calls[1][1] {
		t.Errorf("guake calls = %q, want the same line typed twice", calls)
	}
}

func TestNew_AProjectNameBatonDoesNotKnowIsRefusedWithoutOpeningATab(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))

	res := env.Run("new", "nowhere", "--agent", "claude")

	if res.ExitCode != 1 || res.Stderr != "baton: unknown project \"nowhere\"; pass the path of a git repository to register it\n" {
		t.Errorf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if calls := env.Calls("guake"); len(calls) != 0 {
		t.Errorf("guake was called: %v", calls)
	}
}

func TestNew_ADirectoryOutsideGitIsRefusedWithoutOpeningATab(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	dir := env.Path("plain")
	env.WriteFile(dir+"/file", "x")

	res := env.Run("new", dir, "--agent", "claude")

	if res.ExitCode != 1 || res.Stderr != "baton: "+dir+" is not inside a git repository\n" {
		t.Errorf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if calls := env.Calls("guake"); len(calls) != 0 {
		t.Errorf("guake was called: %v", calls)
	}
}

// twoProfiles adds the default ~/.claude Profile and a work Profile.
func twoProfiles(env *batontest.Env) string {
	return `
[profiles.claude]
agent = "claude"
dir = "` + env.Path(".claude") + `"

[profiles.claude-work]
agent = "claude"
dir = "` + env.Path(".claude-work") + `"
`
}

// command returns the shell line typed into the tab of the only new Session,
// without the leading change of directory.
func command(t *testing.T, env *batontest.Env, repo string) string {
	t.Helper()
	line := typedLine(t, env)
	prefix := "cd " + repo + " && "
	if !strings.HasPrefix(line, prefix) {
		t.Fatalf("line = %q, want it to start with %q", line, prefix)
	}
	return strings.TrimPrefix(line, prefix)
}

// typedLine returns the whole shell line of the only guake call.
func typedLine(t *testing.T, env *batontest.Env) string {
	t.Helper()
	calls := guakeArgs(env)
	if len(calls) != 1 || len(calls[0]) != 4 || calls[0][0] != "-e" || calls[0][2] != "-r" {
		t.Fatalf("guake calls = %q, want one call with -e <line> -r <title>", calls)
	}
	return calls[0][1]
}

func TestNew_TheProfileCanBeChosenAndSetsItsConfigDirectory(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, twoProfiles(env)))
	repo := newRepo(t, env, "my-app")

	res := env.Run("new", repo, "--agent", "claude", "--profile", "claude-work")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := "env -u ANTHROPIC_API_KEY -u OPENAI_API_KEY CLAUDE_CONFIG_DIR=" + env.Path(".claude-work") + " claude"
	if got := command(t, env, repo); got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestNew_TheDefaultProfileCanBeSetInTheConfig(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(`default_profile = "claude-work"` + "\n" + newConfig(env, twoProfiles(env)))
	repo := newRepo(t, env, "my-app")

	res := env.Run("new", repo, "--agent", "claude")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := "env -u ANTHROPIC_API_KEY -u OPENAI_API_KEY CLAUDE_CONFIG_DIR=" + env.Path(".claude-work") + " claude"
	if got := command(t, env, repo); got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestNew_TheFlagBeatsTheConfiguredDefault(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(`default_profile = "claude-work"` + "\n" + newConfig(env, twoProfiles(env)))
	repo := newRepo(t, env, "my-app")

	res := env.Run("new", repo, "--agent", "claude", "--profile", "claude-personal")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := "env -u ANTHROPIC_API_KEY -u OPENAI_API_KEY CLAUDE_CONFIG_DIR=" + env.Path(".claude-personal") + " claude"
	if got := command(t, env, repo); got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestNew_TheDefaultClaudeProfileSetsNoConfigDirectoryAndClearsAStaleOne(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(`default_profile = "claude"` + "\n" + newConfig(env, twoProfiles(env)))
	repo := newRepo(t, env, "my-app")

	res := env.Run("new", repo, "--agent", "claude")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := "env -u ANTHROPIC_API_KEY -u OPENAI_API_KEY -u CLAUDE_CONFIG_DIR claude"
	if got := command(t, env, repo); got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestNew_SeveralProfilesAndNoDefaultAreRefusedWithTheirNames(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, twoProfiles(env)))
	repo := newRepo(t, env, "my-app")

	res := env.Run("new", repo, "--agent", "claude")

	want := "baton: several claude profiles (claude, claude-personal, claude-work); choose one with --profile or set default_profile in the config\n"
	if res.ExitCode != 1 || res.Stderr != want {
		t.Errorf("exit code = %d, stderr=%q, want %q", res.ExitCode, res.Stderr, want)
	}
	if calls := env.Calls("guake"); len(calls) != 0 {
		t.Errorf("guake was called: %v", calls)
	}
}

func TestNew_AnUnknownProfileIsRefusedWithTheKnownOnes(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")

	res := env.Run("new", repo, "--agent", "claude", "--profile", "nope")

	want := "baton: unknown profile \"nope\" (claude profiles: claude-personal)\n"
	if res.ExitCode != 1 || res.Stderr != want {
		t.Errorf("exit code = %d, stderr=%q, want %q", res.ExitCode, res.Stderr, want)
	}
	if calls := env.Calls("guake"); len(calls) != 0 {
		t.Errorf("guake was called: %v", calls)
	}
}

func TestNew_ADefaultProfileThatIsNotConfiguredIsAConfigError(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(`default_profile = "ghost"` + "\n" + newConfig(env, ""))

	res := env.Run("new", env.Path("anything"), "--agent", "claude")

	want := "baton: load config: " + env.ConfigPath() + ": default_profile \"ghost\" is not a configured profile\n"
	if res.ExitCode != 1 || res.Stderr != want {
		t.Errorf("exit code = %d, stderr=%q, want %q", res.ExitCode, res.Stderr, want)
	}
}

func TestNew_ATaskNameGoesIntoTheTabTitle(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")

	res := env.Run("new", repo, "login-fix", "--agent", "claude")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got, want := guakeTitle(t, env), "my-app/login-fix:claude"; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}
}

func TestNew_FlagsMayComeBeforeTheArguments(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, twoProfiles(env)))
	repo := newRepo(t, env, "my-app")

	res := env.Run("new", "--agent=claude", "--profile", "claude-work", repo, "login-fix")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got, want := guakeTitle(t, env), "my-app/login-fix:claude"; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}
}

func TestNew_BadArgumentsAreUsageErrorsAndOpenNoTab(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no project", []string{"new", "--agent", "claude"}, "baton: new: a project is required (usage: baton new <project> [task] --agent <claude|codex> [--profile <name>])\n"},
		{"no agent", []string{"new", "my-app"}, "baton: new: --agent is required (usage: baton new <project> [task] --agent <claude|codex> [--profile <name>])\n"},
		{"too many arguments", []string{"new", "my-app", "a", "b", "--agent", "claude"}, "baton: new: unexpected argument \"b\" (usage: baton new <project> [task] --agent <claude|codex> [--profile <name>])\n"},
		{"unknown agent", []string{"new", "my-app", "--agent", "gemini"}, "baton: new: --agent: unsupported agent \"gemini\" (supported: claude, codex)\n"},
		{"bad task name", []string{"new", "my-app", "Not A Task", "--agent", "claude"}, "baton: new: invalid task name \"Not A Task\": use lowercase letters, digits, '.', '_' and '-', starting with a letter or digit\n"},
		{"unknown flag", []string{"new", "my-app", "--agent", "claude", "--bogus"}, "baton: new: flag provided but not defined: -bogus (usage: baton new <project> [task] --agent <claude|codex> [--profile <name>])\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t)
			env.WriteConfig(newConfig(env, ""))

			res := env.Run(tt.args...)

			if res.ExitCode != 2 || res.Stderr != tt.want {
				t.Errorf("exit code = %d, stderr=%q, want 2 and %q", res.ExitCode, res.Stderr, tt.want)
			}
			if calls := env.Calls("guake"); len(calls) != 0 {
				t.Errorf("guake was called: %v", calls)
			}
		})
	}
}

func TestNew_ARefusedGuakeIsReported(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	env.Stub("guake", batontest.WithScript(`echo "no dbus" >&2; exit 1`))

	res := env.Run("new", repo, "--agent", "claude")

	want := "baton: open a tab for my-app/YYYY-MM-DD-1:claude: guake: exit status 1: no dbus\n"
	if got := stable(res.Stderr); res.ExitCode != 1 || got != want {
		t.Errorf("exit code = %d, stderr=%q, want %q", res.ExitCode, got, want)
	}
}

// typeIntoATab plays what Guake does with the command: its interactive shell
// reads the line, with the API keys and a stale config directory in its
// environment as the long-running Guake server may have them. It returns the
// environment the claude stub saw.
func typeIntoATab(t *testing.T, env *batontest.Env, line string) string {
	t.Helper()
	env.Stub("claude", batontest.WithScript(`env | sort > "$HOME/claude-env.txt"`))
	stale := "export ANTHROPIC_API_KEY=sk-ant-x OPENAI_API_KEY=sk-oai-x CLAUDE_CONFIG_DIR=/stale/dir KEEP_ME=1; "

	res := env.Command("sh", "-c", stale+line)

	if res.ExitCode != 0 {
		t.Fatalf("the line %q failed: exit code = %d, stderr=%q", line, res.ExitCode, res.Stderr)
	}
	return env.ReadFile("claude-env.txt")
}

func TestNew_TheTypedLineRemovesTheAPIKeysAndSetsTheRelocatedProfile(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	env.Run("new", repo, "--agent", "claude")

	seen := typeIntoATab(t, env, typedLine(t, env))

	for _, gone := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
		if strings.Contains(seen, gone) {
			t.Errorf("%s is in the Agent's environment:\n%s", gone, seen)
		}
	}
	if !strings.Contains(seen, "CLAUDE_CONFIG_DIR="+env.Path(".claude-personal")+"\n") {
		t.Errorf("CLAUDE_CONFIG_DIR is not the Profile's directory:\n%s", seen)
	}
	if !strings.Contains(seen, "KEEP_ME=1\n") {
		t.Errorf("an unrelated variable was lost:\n%s", seen)
	}
}

func TestNew_TheTypedLineForTheDefaultProfileLeavesNoConfigDirectory(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(`default_profile = "claude"` + "\n" + newConfig(env, twoProfiles(env)))
	repo := newRepo(t, env, "my-app")
	env.Run("new", repo, "--agent", "claude")

	seen := typeIntoATab(t, env, typedLine(t, env))

	for _, gone := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "CLAUDE_CONFIG_DIR"} {
		if strings.Contains(seen, gone) {
			t.Errorf("%s is in the Agent's environment:\n%s", gone, seen)
		}
	}
}

func TestNew_AProfileDirectoryWithSpacesAndQuotesSurvivesTheShell(t *testing.T) {
	env := newEnv(t)
	dir := env.Path("my profiles", "o'brien $HOME")
	env.WriteConfig(`source = "` + env.SourceDir + `"

[profiles.odd]
agent = "claude"
dir = "` + dir + `"
`)
	repo := newRepo(t, env, "my-app")
	env.Run("new", repo, "--agent", "claude")

	seen := typeIntoATab(t, env, typedLine(t, env))

	if !strings.Contains(seen, "CLAUDE_CONFIG_DIR="+dir+"\n") {
		t.Errorf("CLAUDE_CONFIG_DIR is not %q:\n%s", dir, seen)
	}
}

func TestNew_TheSessionTheTabStartsShowsUpInLsUnderTheProject(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	env.Run("new", repo, "--agent", "claude")

	// What the Claude in the tab reports through its status hooks.
	runHook(t, env, "claude-personal", "SessionStart", "abc123", repo)
	res := env.Run("ls")

	want := "PROJECT  TASK          AGENT   PROFILE          RUNTIME  STATUS\n" +
		"my-app   YYYY-MM-DD-1  claude  claude-personal  host     waiting_input\n"
	if got := stable(res.Stdout); res.ExitCode != 0 || got != want {
		t.Errorf("exit code = %d, stdout=%q, want %q", res.ExitCode, got, want)
	}
	if got := env.ListDir(".baton/projects"); !reflect.DeepEqual(got, []string{".lock", "my-app"}) {
		t.Errorf("projects = %v, want my-app registered once", got)
	}
}

func TestNew_TheTypedLineStartsTheAgentInTheProjectsDirectoryEvenWithOddCharactersInIt(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my app's $HOME")
	env.Run("new", repo, "--agent", "claude")

	typeIntoATab(t, env, typedLine(t, env))

	calls := env.Calls("claude")
	if len(calls) != 1 || calls[0].Dir != repo {
		t.Errorf("claude calls = %v, want one started in %q", calls, repo)
	}
}
