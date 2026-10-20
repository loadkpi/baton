package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loadkpi/baton/internal/batontest"
)

// saveIn runs `baton handoff save` in dir with the narrative on standard input.
func saveIn(env *batontest.Env, dir, narrative string) batontest.Result {
	return env.RunIn(dir, nil, narrative, "handoff", "save")
}

// draftFile is where a narrative saved ahead of the Handoff of the test Task
// is kept.
func draftFile(env *batontest.Env) string {
	return env.Path(".baton", "projects", "my-app", "handoffs", "2031-03-04-1", "draft.md")
}

func TestHandoffSave_OnTheHostWritesTheDraftOfTheCurrentTaskAndALaterSaveReplacesIt(t *testing.T) {
	env := newEnv(t)
	repo := handoffProjectRunning(t, env)

	first := saveIn(env, repo, "## Goal\n\nMake login work.\n")
	second := saveIn(env, filepath.Join(repo, "."), "## Goal\n\nMake logout work.\n")

	if first.ExitCode != 0 || second.ExitCode != 0 {
		t.Fatalf("exit codes = %d, %d; stderr=%q, %q", first.ExitCode, second.ExitCode, first.Stderr, second.Stderr)
	}
	if got, want := env.ReadFile(draftFile(env)), "## Goal\n\nMake logout work.\n"; got != want {
		t.Errorf("draft = %q, want %q", got, want)
	}
	if mode := env.Mode(draftFile(env)); mode != 0o600 {
		t.Errorf("draft mode = %o, want 600", mode)
	}
	if !strings.Contains(first.Stdout, draftFile(env)) {
		t.Errorf("stdout = %q, want it to name the draft %s", first.Stdout, draftFile(env))
	}
	for _, name := range env.ListDir(filepath.Dir(draftFile(env))) {
		if strings.Contains(name, ".tmp") {
			t.Errorf("a temporary file is left: %s", name)
		}
	}
	if _, err := os.Stat(handoffFile(env, "1")); err == nil {
		t.Error("a Handoff was written by save")
	}
}

func TestHandoffSave_EmptyInputIsAnErrorAndWritesNothing(t *testing.T) {
	env := newEnv(t)
	repo := handoffProjectRunning(t, env)

	for _, input := range []string{"", " \n\t\n"} {
		res := saveIn(env, repo, input)

		if res.ExitCode != 1 || !strings.Contains(res.Stderr, "empty") {
			t.Errorf("input %q: exit code = %d, stderr=%q, want 1 and a message that the narrative is empty", input, res.ExitCode, res.Stderr)
		}
	}
	if env.Exists(draftFile(env)) {
		t.Error("an empty narrative was saved")
	}
}

func TestHandoffSave_OutsideAnyTaskIsAnError(t *testing.T) {
	env := newEnv(t)
	handoffProjectRunning(t, env)
	elsewhere := t.TempDir()

	res := saveIn(env, elsewhere, "text\n")

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "not inside a git repository") {
		t.Errorf("exit code = %d, stderr=%q, want 1 and a message that the directory is not in git", res.ExitCode, res.Stderr)
	}
}

// narrativePart is what follows the narrative heading of a Handoff.
func narrativePart(t *testing.T, handoff string) string {
	t.Helper()
	_, after, ok := strings.Cut(handoff, "\n## Narrative\n")
	if !ok {
		t.Fatalf("no narrative section in:\n%s", handoff)
	}
	return after
}

func TestHandoffSave_BeforeAndAfterStartingHandoffGiveTheSameHandoff(t *testing.T) {
	const narrative = "## Goal\n\nMake login work.\n\n## Next step\n\nRun `go test ./...`.\n"

	// Before: the narrative is saved, then the Session ends and baton handoff runs.
	before := newEnv(t)
	repo := handoffProjectRunning(t, before)
	if res := saveIn(before, repo, narrative); res.ExitCode != 0 {
		t.Fatalf("save: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	endFirstSession(t, before, repo)
	if res := before.Run("handoff", handoffTask, "--to", "claude@claude-work"); res.ExitCode != 0 {
		t.Fatalf("handoff: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	// After: baton handoff is started and waits; the narrative is saved
	// meanwhile; then the Session ends.
	after := newEnv(t)
	repo = handoffProjectRunning(t, after)
	p := after.Start("handoff", handoffTask, "--to", "claude@claude-work")
	p.WaitStdout("waiting for the claude session s1")
	if res := saveIn(after, repo, narrative); res.ExitCode != 0 {
		t.Fatalf("save: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	endFirstSession(t, after, repo)
	if res := p.Wait(); res.ExitCode != 0 {
		t.Fatalf("handoff: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	for name, env := range map[string]*batontest.Env{"before": before, "after": after} {
		got := env.ReadFile(handoffFile(env, "1"))
		if !strings.Contains(got, "narrative: handing-over\n") {
			t.Errorf("%s: handoff lacks narrative: handing-over:\n%s", name, got)
		}
		if got := narrativePart(t, got); got != "\n"+narrative {
			t.Errorf("%s: narrative part = %q, want %q", name, got, "\n"+narrative)
		}
		if env.Exists(draftFile(env)) {
			t.Errorf("%s: draft.md is still there", name)
		}
	}
}

func TestHandoffSave_AfterAHandoffWithNoNarrativeFillsItAsTheReceiver(t *testing.T) {
	env := newEnv(t)
	repo := handoffProject(t, env)
	if res := env.Run("handoff", handoffTask, "--to", "claude@claude-work"); res.ExitCode != 0 {
		t.Fatalf("handoff: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	before := env.ReadFile(handoffFile(env, "1"))

	res := saveIn(env, repo, "## Goal\n\nMake login work.\n")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	got := env.ReadFile(handoffFile(env, "1"))
	if !strings.Contains(got, "narrative: receiving\n") || strings.Contains(got, "narrative: missing") {
		t.Errorf("handoff header does not say receiving:\n%s", got)
	}
	if want := "\n## Narrative\n\n## Goal\n\nMake login work.\n"; !strings.HasSuffix(got, want) {
		t.Errorf("handoff does not end with the narrative under its heading:\n%s", got)
	}
	// Only the header line and the narrative part changed.
	factsBefore := strings.Replace(strings.SplitN(before, "\n## Narrative\n", 2)[0], "narrative: missing", "narrative: receiving", 1)
	if factsOf := strings.SplitN(got, "\n## Narrative\n", 2)[0]; factsOf != factsBefore {
		t.Errorf("the facts changed:\n%s\nwant\n%s", factsOf, factsBefore)
	}
	if mode := env.Mode(handoffFile(env, "1")); mode != 0o600 {
		t.Errorf("mode = %o, want 600", mode)
	}
	if env.Exists(draftFile(env)) {
		t.Error("a draft was written next to the filled Handoff")
	}
	if !strings.Contains(res.Stdout, handoffFile(env, "1")) {
		t.Errorf("stdout = %q, want it to name %s", res.Stdout, handoffFile(env, "1"))
	}
}

func TestHandoffSave_AfterAHandoffWithANarrativeStartsADraftForTheNextOne(t *testing.T) {
	env := newEnv(t)
	repo := handoffProject(t, env)
	env.WriteFile(draftFile(env), "## Goal\n\nOld.\n")
	if res := env.Run("handoff", handoffTask, "--to", "claude@claude-work"); res.ExitCode != 0 {
		t.Fatalf("handoff: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	first := env.ReadFile(handoffFile(env, "1"))

	res := saveIn(env, repo, "## Goal\n\nNew.\n")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := env.ReadFile(handoffFile(env, "1")); got != first {
		t.Errorf("Handoff 1 changed:\n%s", got)
	}
	if got, want := env.ReadFile(draftFile(env)), "## Goal\n\nNew.\n"; got != want {
		t.Errorf("draft = %q, want %q", got, want)
	}
}

func TestHandoffSave_TheHandoffWithNoNarrativeAsksTheReceiverToWriteItInTheFirstMessage(t *testing.T) {
	env := newEnv(t)
	handoffProject(t, env)

	res := env.Run("handoff", handoffTask, "--to", "claude@claude-work")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	line := typedLine(t, env)
	if strings.Contains(line, "\n") {
		t.Errorf("tab line = %q, want one line", line)
	}
	if want := "claude 'continue the Task from Handoff " + handoffFile(env, "1") + askForNarrative + "'"; !strings.HasSuffix(line, want) {
		t.Errorf("tab line = %q, want it to end with %q", line, want)
	}
}

// saveInContainer plays an Agent in the sandbox container of Project app
// calling /baton/bin/baton handoff save. Its working directory is a path of
// the container, which does not exist on the host, so it is HOME here.
func saveInContainer(env *batontest.Env, root, narrative string) batontest.Result {
	return env.RunIn(env.Home, []string{"BATON_CONTAINER_ROOT=" + root}, narrative, "handoff", "save")
}

func TestHandoffSave_InTheSandboxFindsTheTaskThroughTheMountAndWritesTheDraftThere(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	if res := env.Run("new", "app", "--agent", "claude"); res.ExitCode != 0 {
		t.Fatalf("new: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	root := containerRoot(t, env, "app")
	tasks := env.ListDir(env.Path(".baton", "projects", "app", "tasks"))
	if len(tasks) != 1 {
		t.Fatalf("tasks = %v, want one", tasks)
	}
	task := strings.TrimSuffix(tasks[0], ".toml")
	draft := env.Path(".baton", "projects", "app", "handoffs", task, "draft.md")

	res := saveInContainer(env, root, "## Goal\n\nMake login work.\n")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got, want := env.ReadFile(draft), "## Goal\n\nMake login work.\n"; got != want {
		t.Errorf("draft = %q, want %q", got, want)
	}
	if mode := env.Mode(draft); mode != 0o600 {
		t.Errorf("draft mode = %o, want 600", mode)
	}
	if !strings.Contains(res.Stdout, filepath.Join(root, "baton", "project", "handoffs", task, "draft.md")) {
		t.Errorf("stdout = %q, want it to name the draft as the container sees it", res.Stdout)
	}
}

func TestHandoffSave_InTheSandboxTheReceiverFillsTheHandoffThatHasNoNarrative(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	task := codexSession(t, env)
	if res := env.Run("handoff", "app/"+task, "--to", "codex"); res.ExitCode != 0 {
		t.Fatalf("handoff: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	file := env.Path(".baton", "projects", "app", "handoffs", task, "1.md")
	if !strings.Contains(env.ReadFile(file), "narrative: missing\n") {
		t.Fatalf("handoff = %q, want narrative: missing", env.ReadFile(file))
	}
	root := containerRoot(t, env, "app")

	res := saveInContainer(env, root, "## Next step\n\nRun the tests.\n")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	got := env.ReadFile(file)
	if !strings.Contains(got, "narrative: receiving\n") || !strings.HasSuffix(got, "\n## Narrative\n\n## Next step\n\nRun the tests.\n") {
		t.Errorf("handoff not filled by the receiver:\n%s", got)
	}
}

func TestHandoffSave_InTheSandboxWithoutAProjectRecordIsAnError(t *testing.T) {
	env := newEnv(t)
	root := containerRoot(t, env, "app") // a mount with nothing of Baton in it

	res := saveInContainer(env, root, "text\n")

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "project.toml") {
		t.Errorf("exit code = %d, stderr=%q, want 1 and a message about project.toml", res.ExitCode, res.Stderr)
	}
}

func TestHandoffSave_TheContainerRootMeansNothingWhereTheMountIsMissing(t *testing.T) {
	env := newEnv(t)
	repo := handoffProjectRunning(t, env)
	root := t.TempDir() // no /baton/project in it: this is the host

	res := env.RunIn(repo, []string{"BATON_CONTAINER_ROOT=" + root}, "## Goal\n", "handoff", "save")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if !env.Exists(draftFile(env)) {
		t.Error("the draft was not written on the host")
	}
}
