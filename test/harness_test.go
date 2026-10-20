package test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/loadkpi/baton/internal/batontest"
)

// These tests cover the harness itself, because every later ticket relies on
// its stubs and its isolation.

func TestHarness_StubsRecordCallsInsteadOfRunningRealPrograms(t *testing.T) {
	env := newEnv(t)

	res := env.Command("guake", "-n", "NEW_TAB", "-r", "proj/task:claude", "-e", "claude --flag 'two words'")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	calls := env.Calls("guake")
	want := []batontest.Call{{
		Args: []string{"-n", "NEW_TAB", "-r", "proj/task:claude", "-e", "claude --flag 'two words'"},
		Dir:  env.Home,
	}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %#v, want %#v", calls, want)
	}
}

func TestHarness_StubScriptControlsOutputAndExitCode(t *testing.T) {
	env := newEnv(t)
	env.Stub("docker", batontest.WithScript(`echo "container-1"; exit 3`))

	res := env.Command("docker", "ps")
	env.Command("docker", "ps", "-a")

	if res.Stdout != "container-1\n" || res.ExitCode != 3 {
		t.Errorf("stdout=%q exit=%d, want %q and 3", res.Stdout, res.ExitCode, "container-1\n")
	}
	if got := len(env.Calls("docker")); got != 2 {
		t.Errorf("recorded %d docker calls, want 2", got)
	}
}

func TestHarness_ProcessesSeeOnlyTheIsolatedEnvironment(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-real-key")
	env := newEnv(t)

	res := env.Command("sh", "-c", `printf '%s|%s' "$HOME" "$ANTHROPIC_API_KEY"`)

	if want := env.Home + "|"; res.Stdout != want {
		t.Errorf("HOME and key = %q, want %q", res.Stdout, want)
	}
}

func TestHarness_SourceIsAGitRepositoryWithCommits(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.CommitSource("add skill")

	res := env.Command("git", "-C", env.SourceDir, "log", "--format=%s")

	if got := strings.TrimSpace(res.Stdout); got != "add skill" {
		t.Errorf("git log = %q, want %q", got, "add skill")
	}
}
