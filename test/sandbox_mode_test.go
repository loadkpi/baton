package test

import (
	"strings"
	"testing"

	"github.com/loadkpi/baton/internal/batontest"
)

// withSandboxMode adds the table of Project app to the config launchProject
// wrote.
func withSandboxMode(env *batontest.Env, table string) {
	env.WriteConfig(standardConfig(env) + "\n[projects.app]\n" + table)
}

// modeOf is the mode a docker run command was built for: run.sh passes it to
// the container as AI_SANDBOX_MODE.
func modeOf(t *testing.T, args []string) string {
	t.Helper()
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-e" && strings.HasPrefix(args[i+1], "AI_SANDBOX_MODE=") {
			return strings.TrimPrefix(args[i+1], "AI_SANDBOX_MODE=")
		}
	}
	t.Fatalf("docker args = %q, want run.sh's AI_SANDBOX_MODE", args)
	return ""
}

const codexCannotRun = "Codex's own sandbox cannot start inside the container"

func TestSandboxMode_NewPassesTheConfiguredModeToRunSh(t *testing.T) {
	env := newEnv(t)
	_, home := launchProject(t, env)
	withSandboxMode(env, "sandbox_mode = \"offline\"\n")

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := modeOf(t, runTabLine(t, env)); got != "offline" {
		t.Errorf("mode of the container = %q, want offline", got)
	}
	if text := env.ReadFile(home + "/.codex/config.toml"); !strings.Contains(text, `sandbox_mode = "danger-full-access"`) {
		t.Errorf("rendered Codex config = %q, want what run.sh renders for that mode", text)
	}
}

func TestSandboxMode_WithoutTheKeyRunShGetsNoMode(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	withSandboxMode(env, "tags = [\"work\"]\n")

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := modeOf(t, runTabLine(t, env)); got != "default" {
		t.Errorf("mode of the container = %q, want run.sh's own default", got)
	}
}

func TestSandboxMode_AnUnknownModeFailsInRunShAndItsMessageIsShown(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	withSandboxMode(env, "sandbox_mode = \"nonsense\"\n")

	res := env.Run("new", "app", "--agent", "claude")

	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "Unknown mode: nonsense") {
		t.Errorf("exit code = %d, stderr=%q, want a failure with run.sh's message", res.ExitCode, res.Stderr)
	}
	if calls := guakeArgs(env); len(calls) != 0 {
		t.Errorf("guake was called: %v", calls)
	}
}

func TestSandboxMode_InvalidConfigIsAnErrorThatNamesTheProject(t *testing.T) {
	cases := []struct {
		name, table, wantErr string
	}{
		{"empty", "sandbox_mode = \"\"\n", "projects.app: sandbox_mode: \"\" must be a non-empty word of a-z, 0-9 and -, not starting with -"},
		{"upper case", "sandbox_mode = \"Dev\"\n", "projects.app: sandbox_mode: \"Dev\" must be a non-empty word of a-z, 0-9 and -, not starting with -"},
		{"flag", "sandbox_mode = \"--dev\"\n", "projects.app: sandbox_mode: \"--dev\" must be a non-empty word of a-z, 0-9 and -, not starting with -"},
		{"not a string", "sandbox_mode = 3\n", "config.toml"},
		{"compose project", "sandbox_mode = \"dev\"\n[projects.app.compose]\nfiles = [\"compose.yml\"]\nservice = \"web\"\n[projects.app.compose.profiles.main]\nagent = \"claude\"\ndir = \"~/.claude\"\n",
			"projects.app: sandbox_mode applies to a Project with a sandbox, not to one with a Compose service"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newEnv(t)
			launchProject(t, env)
			withSandboxMode(env, tc.table)

			res := env.Run("new", "app", "--agent", "claude")

			if res.ExitCode == 0 || !strings.Contains(res.Stderr, tc.wantErr) {
				t.Errorf("exit code = %d, stderr=%q, want a failure containing %q", res.ExitCode, res.Stderr, tc.wantErr)
			}
			if calls := guakeArgs(env); len(calls) != 0 {
				t.Errorf("guake was called: %v", calls)
			}
		})
	}
}

func TestSandboxMode_CodexWithWorkspaceWriteGetsAWarningAndStillLaunches(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)

	res := env.Run("new", "app", "--agent", "codex")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := "baton: warning: project app: .ai-sandbox/run.sh rendered sandbox_mode = \"workspace-write\" for Codex, " + codexCannotRun +
		", so its shell commands will fail; set sandbox_mode = \"dev\" (or another mode whose run.sh gives danger-full-access) under [projects.app], or update .ai-sandbox/run.sh\n"
	if !strings.Contains(res.Stderr, want) {
		t.Errorf("stderr = %q, want it to contain %q", res.Stderr, want)
	}
	if calls := guakeArgs(env); len(calls) != 1 {
		t.Errorf("guake calls = %v, want the launch to go ahead", calls)
	}
}

func TestSandboxMode_NoWarningWhenCodexGetsFullAccessOrTheAgentIsClaude(t *testing.T) {
	cases := []struct {
		name, agent, table string
	}{
		{"codex in dev", "codex", "sandbox_mode = \"dev\"\n"},
		{"claude in default", "claude", "tags = []\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newEnv(t)
			launchProject(t, env)
			withSandboxMode(env, tc.table)

			res := env.Run("new", "app", "--agent", tc.agent)

			if res.ExitCode != 0 {
				t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
			}
			if strings.Contains(res.Stderr, codexCannotRun) {
				t.Errorf("stderr = %q, want no warning", res.Stderr)
			}
		})
	}
}

func TestSandboxMode_HandoffToCodexPassesTheModeAndWarnsLikeNew(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	task := codexSession(t, env)
	withSandboxMode(env, "sandbox_mode = \"open\"\n")

	res := env.Run("handoff", "app/"+task, "--to", "codex")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if strings.Contains(res.Stderr, codexCannotRun) {
		t.Errorf("stderr = %q, want no warning for a mode with full access", res.Stderr)
	}
	calls := guakeArgs(env)
	if len(calls) != 2 {
		t.Fatalf("guake calls = %q, want two", calls)
	}
	if res := env.Command("sh", "-c", calls[1][1]); res.ExitCode != 0 {
		t.Fatalf("the tab line failed: %s", res.Stderr)
	}
	runs := dockerCalls(env, "run")
	if got := modeOf(t, runs[len(runs)-1]); got != "open" {
		t.Errorf("mode of the container = %q, want open", got)
	}
}

func TestSandboxMode_HandoffToCodexWarnsWhenTheModeKeepsWorkspaceWrite(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	task := codexSession(t, env)

	res := env.Run("handoff", "app/"+task, "--to", "codex")

	if res.ExitCode != 0 || !strings.Contains(res.Stderr, codexCannotRun) {
		t.Errorf("exit code = %d, stderr=%q, want the warning", res.ExitCode, res.Stderr)
	}
}
