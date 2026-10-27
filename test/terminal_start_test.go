package test

import (
	"strings"
	"testing"

	"github.com/loadkpi/baton/internal/batontest"
)

// terminalConfig is the standard config with a start timeout: short where a
// test expects the launched program never to show up, a few seconds where it
// has to be found while the stub starts it.
func terminalConfig(env *batontest.Env, timeout string) string {
	return standardConfig(env) + "\n[terminal]\nstart_timeout = \"" + timeout + "\"\n"
}

// notStartedMessage is what baton says when the program of a tab never ran.
func notStartedMessage(title, timeout, line string) string {
	return "baton: the command did not start in the new Guake tab " + title + " within " + timeout + ".\n" +
		"Something in the shell's startup may have read the terminal (for example the oh-my-zsh dotenv plugin asking about a .env file).\n" +
		"Run this line by hand in that tab, or try again:\n" +
		line + "\n"
}

func TestTerminalStart_AHostLaunchWhoseAgentRunsInTheTabsDirectoryIsFound(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(terminalConfig(env, "5s"))
	env.Stub("claude", batontest.WithScript("sleep 30"))
	env.Stub("guake", env.GuakeRunsLine())
	repo := newRepo(t, env, "my-app")

	res := env.Run("new", repo, "login-fix", "--agent", "claude")

	if res.ExitCode != 0 || res.Stderr != "" {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if calls := env.Calls("claude"); len(calls) != 1 || calls[0].Dir != repo {
		t.Errorf("claude calls = %v, want one, in %s", calls, repo)
	}
}

func TestTerminalStart_AHostLaunchWhoseAgentNeverRunsFailsWithTheLineToPasteAndTheTabTitle(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(terminalConfig(env, "300ms"))
	env.Stub("guake")
	repo := newRepo(t, env, "my-app")

	res := env.Run("new", repo, "login-fix", "--agent", "claude")

	line := "cd " + repo + " && env -u ANTHROPIC_API_KEY -u OPENAI_API_KEY CLAUDE_CONFIG_DIR=" + env.Path(".claude-personal") + " claude"
	if want := notStartedMessage("my-app/login-fix:claude", "300ms", line); res.ExitCode != 1 || res.Stderr != want {
		t.Errorf("exit code = %d, stderr = %q, want 1 and %q", res.ExitCode, res.Stderr, want)
	}
	if res.Stdout != "" {
		t.Errorf("stdout = %q, want none", res.Stdout)
	}
}

func TestTerminalStart_AProgramWithTheRightNameInAnotherDirectoryDoesNotCount(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(terminalConfig(env, "300ms"))
	env.Stub("guake", env.GuakeStartsIn(env.Home, "claude"))
	repo := newRepo(t, env, "my-app")

	res := env.Run("new", repo, "login-fix", "--agent", "claude")

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "did not start in the new Guake tab my-app/login-fix:claude") {
		t.Errorf("exit code = %d, stderr = %q, want 1 and the message", res.ExitCode, res.Stderr)
	}
}

func TestTerminalStart_AProgramWithAnotherNameInTheTabsDirectoryDoesNotCount(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(terminalConfig(env, "300ms"))
	env.Stub("guake", env.GuakeStarts("docker", "codex"))
	repo := newRepo(t, env, "my-app")

	res := env.Run("new", repo, "login-fix", "--agent", "claude")

	if res.ExitCode != 1 {
		t.Errorf("exit code = %d, stderr = %q, want 1", res.ExitCode, res.Stderr)
	}
}

func TestTerminalStart_ASandboxLaunchExpectsDocker(t *testing.T) {
	env := newEnv(t)
	repo, _ := launchProject(t, env)
	env.WriteConfig(terminalConfig(env, "5s"))
	env.Stub("guake", env.GuakeStarts("docker"))

	res := env.Run("new", "app", "--agent", "claude")
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	env.WriteConfig(terminalConfig(env, "300ms"))
	env.Stub("guake", env.GuakeStartsIn(repo, "claude"))
	res = env.Run("new", "app", "--agent", "claude")
	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "did not start in the new Guake tab") {
		t.Errorf("claude in the tab, no docker: exit code = %d, stderr = %q, want 1 and the message", res.ExitCode, res.Stderr)
	}
}

func TestTerminalStart_AComposeLaunchExpectsDocker(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(composeConfig(env, "") + "\n[terminal]\nstart_timeout = \"5s\"\n")
	repo := composeProject(t, env, "app")
	batonMountedContainer(env, repo, true)
	env.Stub("guake", env.GuakeStarts("docker"))

	res := env.Run("new", "app", "--agent", "claude")
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	env.WriteConfig(composeConfig(env, "") + "\n[terminal]\nstart_timeout = \"300ms\"\n")
	env.Stub("guake", env.GuakeStarts("claude"))
	res = env.Run("new", "app", "--agent", "claude")
	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "did not start in the new Guake tab") {
		t.Errorf("claude in the tab, no docker: exit code = %d, stderr = %q, want 1 and the message", res.ExitCode, res.Stderr)
	}
}

func TestTerminalStart_AHandoffWhoseTabDidNotStartKeepsTheHandoffAndSaysWhereItIs(t *testing.T) {
	env := newEnv(t)
	repo := handoffProject(t, env)
	env.WriteConfig(terminalConfig(env, "300ms") + `
[profiles.claude-work]
agent = "claude"
dir = "` + env.Path(".claude-work") + `"
`)
	env.Stub("guake")

	res := env.Run("handoff", handoffTask, "--to", "claude@claude-work")

	path := handoffFile(env, "1")
	if res.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", res.ExitCode)
	}
	if !env.Exists(path) {
		t.Errorf("the Handoff %s was removed", path)
	}
	for _, want := range []string{
		"baton: the Handoff is written to " + path + ", but the receiving Session was not started: ",
		"the command did not start in the new Guake tab my-app/2031-03-04-1:claude within 300ms.",
		"cd " + repo + " && env -u ANTHROPIC_API_KEY",
	} {
		if !strings.Contains(res.Stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, want)
		}
	}
}

func TestTerminalConfig_AStartTimeoutThatIsNotAPositiveDurationIsRefused(t *testing.T) {
	for _, value := range []string{`"soon"`, `"0s"`, `"-5s"`, `10`} {
		t.Run(value, func(t *testing.T) {
			env := newEnv(t)
			env.WriteConfig(standardConfig(env) + "\n[terminal]\nstart_timeout = " + value + "\n")
			repo := newRepo(t, env, "my-app")

			res := env.Run("new", repo, "--agent", "claude")

			if res.ExitCode != 1 || !strings.Contains(res.Stderr, "terminal.start_timeout") {
				t.Errorf("exit code = %d, stderr = %q, want 1 and a message that names terminal.start_timeout", res.ExitCode, res.Stderr)
			}
			if len(env.Calls("guake")) != 0 {
				t.Error("guake was called")
			}
		})
	}
}
