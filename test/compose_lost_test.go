package test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/loadkpi/baton/internal/batontest"
)

// composeEvent appends an Event of a Claude Session of Project app in its
// Compose container, as the hook there would have written it.
func composeEvent(t *testing.T, env *batontest.Env, sessionID, ts, event string, p proc) {
	t.Helper()
	writeProcEvent(t, env, ".baton/projects/app/sessions/claude-"+sessionID+".jsonl", "compose", "app.claude", sessionID, ts, event, "/workspace", p)
}

// composeLostProject is Project "app" with its Compose config and a running
// container that has Baton's mounts and the checkout at /workspace.
func composeLostProject(t *testing.T, env *batontest.Env) (repo string, c *batontest.Container) {
	t.Helper()
	env.WriteConfig(composeConfig(env, ""))
	repo = composeProject(t, env, "app")
	return repo, batonMountedContainer(env, repo, true)
}

// aaaa is the host name inside the first fake container: the first 12
// characters of its id, which is what Docker gives a container.
const aaaa = "aaaaaaaaaaaa"

func TestComposeLost_ASessionIsLostWhenItsPIDIsGoneFromTheRunningContainerOrIsAnotherProcessNowOrAZombie(t *testing.T) {
	env := newEnv(t)
	_, c := composeLostProject(t, env)
	c.Process(100, "claude", 5000)             // s1: the Agent
	c.Process(300, "bash", 9999)               // s3: the PID was reused
	c.ProcessInState(400, "claude", "Z", 7000) // s4: killed, not reaped by its parent
	// s2 recorded PID 200, which runs nothing.
	composeEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: aaaa, pid: 100, start: 5000})
	composeEvent(t, env, "s2", "2031-03-04T10:01:00Z", "PreToolUse", proc{host: aaaa, pid: 200, start: 6000})
	composeEvent(t, env, "s3", "2031-03-04T10:02:00Z", "Stop", proc{host: aaaa, pid: 300, start: 6000})
	composeEvent(t, env, "s4", "2031-03-04T10:03:00Z", "PreToolUse", proc{host: aaaa, pid: 400, start: 7000})

	res := env.Run("ls")
	rows := lsRows(t, env) // the most recent first: s4, s3, s2, s1

	if want := []string{"lost", "lost", "lost", "running"}; !reflect.DeepEqual(statuses(rows), want) {
		t.Errorf("statuses = %v, want %v", statuses(rows), want)
	}
	if res.Stderr != "" {
		t.Errorf("stderr = %q, want no warning: every Session could be judged", res.Stderr)
	}
}

func TestComposeLost_ASessionIsLostWhenItsContainerIsStoppedOrGone(t *testing.T) {
	env := newEnv(t)
	_, c := composeLostProject(t, env)
	c.Process(100, "claude", 5000)
	composeEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: aaaa, pid: 100, start: 5000})
	running := lsRows(t, env)

	c.SetRunning(false)
	stopped := lsRows(t, env)
	c.Remove()
	removed := lsRows(t, env)

	// A container that is gone cannot place the Session, which is then listed
	// without a Project (a known gap, DESIGN.md, ticket 17); its status is
	// the point here.
	if running[0][6] != "running" || stopped[0][6] != "lost" || removed[0][6] != "lost" {
		t.Errorf("statuses = %v, %v, %v; want running, lost, lost", running, stopped, removed)
	}
}

func TestComposeLost_NothingIsRunInAContainerForSessionsThatEndedAndOneExecServesAllTheSessionsOfAContainer(t *testing.T) {
	env := newEnv(t)
	_, c := composeLostProject(t, env)
	c.Process(100, "claude", 5000)
	c.Process(101, "claude", 5001)
	composeEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: aaaa, pid: 100, start: 5000})
	composeEvent(t, env, "s2", "2031-03-04T10:01:00Z", "PreToolUse", proc{host: aaaa, pid: 101, start: 5001})
	composeEvent(t, env, "s3", "2031-03-04T10:02:00Z", "SessionEnd", proc{host: aaaa, pid: 102, start: 5002})
	before := len(dockerCalls(env, "exec"))

	lsRows(t, env)

	execs := dockerCalls(env, "exec")[before:]
	if len(execs) != 1 {
		t.Fatalf("docker exec calls = %q, want one for the container", execs)
	}
	if got := execs[0]; got[1] != c.ID() || !reflect.DeepEqual(got[6:], []string{"100", "101"}) {
		t.Errorf("docker exec call = %q, want container %s and the PIDs 100 and 101 of the Sessions that have not ended", got, c.ID())
	}
	if !strings.HasPrefix(c.ID(), aaaa) {
		t.Fatal("the fixture's host name is not the container id's start")
	}
}

// eventsOf are the Events of the Session in the Project's Event file, as the
// JSON objects the hook wrote.
func eventsOf(t *testing.T, env *batontest.Env, project, sessionID string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimRight(env.ReadFile(".baton/projects/"+project+"/sessions/claude-"+sessionID+".jsonl"), "\n"), "\n") {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("Event line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

func TestComposeHook_RecordsTheIDOfTheContainerItRunsInWhereTheKernelShowsIt(t *testing.T) {
	id := strings.Repeat("ab12", 16)
	other := strings.Repeat("cd34", 16)
	cases := []struct {
		name, file, content string
	}{
		{"the bind mounts of the container's files", "proc/self/mountinfo",
			"22 1 0:21 / /proc rw - proc proc rw\n" +
				"600 590 8:1 /var/lib/docker/containers/" + id + "/hostname /etc/hostname rw,relatime - ext4 /dev/sda1 rw\n" +
				"601 590 8:1 /var/lib/docker/containers/" + id + "/hosts /etc/hosts rw,relatime - ext4 /dev/sda1 rw\n"},
		{"the cgroup of a systemd host", "proc/self/cgroup", "0::/system.slice/docker-" + id + ".scope\n"},
		{"the cgroup of cgroup v1", "proc/self/cgroup", "12:memory:/docker/" + id + "\n11:cpu:/docker/" + id + "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newEnv(t)
			_, root := startedByHand(t, env)
			env.WriteFile(filepath.Join(root, tc.file), tc.content)

			composeHook(t, env, root, "SessionStart", "s1", "/workspace")

			if got := eventsOf(t, env, "app", "s1")[0]["container"]; got != id {
				t.Errorf("container = %v, want %s", got, id)
			}
		})
	}

	t.Run("a mount of another container's directory is not the container", func(t *testing.T) {
		env := newEnv(t)
		_, root := startedByHand(t, env)
		// The user mounted a directory of another container somewhere.
		env.WriteFile(filepath.Join(root, "proc/self/mountinfo"),
			"600 590 8:1 /var/lib/docker/containers/"+other+"/logs /var/log/other rw,relatime - ext4 /dev/sda1 rw\n")

		composeHook(t, env, root, "SessionStart", "s1", "/workspace")

		if e := eventsOf(t, env, "app", "s1")[0]; e["container"] != nil || e["v"] != float64(1) {
			t.Errorf("Event = %v, want no container field and v 1", e)
		}
	})

	t.Run("nothing to see leaves the field out", func(t *testing.T) {
		env := newEnv(t)
		_, root := startedByHand(t, env)

		composeHook(t, env, root, "SessionStart", "s1", "/workspace")

		if _, has := eventsOf(t, env, "app", "s1")[0]["container"]; has {
			t.Errorf("Event = %v, want no container field", eventsOf(t, env, "app", "s1")[0])
		}
	})
}

func TestComposeLost_AContainerWithAHostNameOfItsOwnIsFoundByTheIDItsHookRecorded(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(composeConfig(env, ""))
	repo := composeProject(t, env, "app")
	env.WriteFile(".baton/bin/baton", "#!/bin/sh\n", true)
	c := env.AddContainer(batontest.ContainerSpec{
		Service: "app", Running: true, Hostname: "dev-box",
		Binds:   map[string]string{repo: "/workspace", env.Path(".baton/projects/app"): "/baton/project"},
		BindsRO: map[string]string{env.Path(".baton/bin"): "/baton/bin"},
	})
	c.Process(100, "claude", 5000)
	composeEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: "dev-box", container: c.ID(), pid: 100, start: 5000})
	composeEvent(t, env, "s2", "2031-03-04T10:01:00Z", "PreToolUse", proc{host: "dev-box", container: c.ID(), pid: 200, start: 6000}) // runs nothing

	res := env.Run("ls")
	rows := lsRows(t, env) // s2, s1

	if want := []string{"lost", "running"}; !reflect.DeepEqual(statuses(rows), want) {
		t.Errorf("statuses = %v, want %v", statuses(rows), want)
	}
	if res.Stderr != "" {
		t.Errorf("stderr = %q, want no warning", res.Stderr)
	}
}

func TestComposeLost_ARecreatedContainerHasANewIDSoTheSessionsOfTheOldOneAreLostAndNewOnesAreTrackedUnderTheNewOne(t *testing.T) {
	env := newEnv(t)
	// The service has a host name of its own, which a new container shares.
	env.WriteConfig(composeConfig(env, ""))
	repo := composeProject(t, env, "app")
	env.WriteFile(".baton/bin/baton", "#!/bin/sh\n", true)
	spec := batontest.ContainerSpec{
		Service: "app", Running: true, Hostname: "dev-box",
		Binds:   map[string]string{repo: "/workspace", env.Path(".baton/projects/app"): "/baton/project"},
		BindsRO: map[string]string{env.Path(".baton/bin"): "/baton/bin"},
	}
	old := env.AddContainer(spec)
	old.Process(100, "claude", 5000)
	composeEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: "dev-box", container: old.ID(), pid: 100, start: 5000})
	if got := statuses(lsRows(t, env)); !reflect.DeepEqual(got, []string{"running"}) {
		t.Fatalf("statuses before the recreate = %v, want running", got)
	}

	// `docker compose up -d` after a change of the service: another container
	// from the same definition. Its PID 100 is another process.
	old.Remove()
	spec.Created = "2031-03-04T12:00:00Z"
	fresh := env.AddContainer(spec)
	fresh.Process(100, "claude", 5000) // the same PID and start tick, in another container
	composeEvent(t, env, "s2", "2031-03-04T13:00:00Z", "PreToolUse", proc{host: "dev-box", container: fresh.ID(), pid: 100, start: 5000})

	rows := lsRows(t, env) // s2, s1

	if want := []string{"running", "lost"}; !reflect.DeepEqual(statuses(rows), want) {
		t.Errorf("statuses = %v, want the new Session running and the old one lost", statuses(rows))
	}
}

// namedContainer is the running container of service app that a Compose file
// gave the host name "dev-box", created at created, with Baton's mounts.
func namedContainer(t *testing.T, env *batontest.Env, created string) *batontest.Container {
	t.Helper()
	env.WriteConfig(composeConfig(env, ""))
	repo := composeProject(t, env, "app")
	env.WriteFile(".baton/bin/baton", "#!/bin/sh\n", true)
	return env.AddContainer(batontest.ContainerSpec{
		Service: "app", Running: true, Hostname: "dev-box", Created: created,
		Binds:   map[string]string{repo: "/workspace", env.Path(".baton/projects/app"): "/baton/project"},
		BindsRO: map[string]string{env.Path(".baton/bin"): "/baton/bin"},
	})
}

func TestComposeLost_WithoutARecordedIDAHostNameOfItsOwnIsMatchedToTheServicesContainerWhenItIsThatContainersConfiguredHostName(t *testing.T) {
	env := newEnv(t)
	c := namedContainer(t, env, "2031-03-04T09:00:00Z")
	c.Process(100, "claude", 5000)
	// Events written by a hook that could not tell the container id.
	composeEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: "dev-box", pid: 100, start: 5000})
	composeEvent(t, env, "s2", "2031-03-04T10:01:00Z", "PreToolUse", proc{host: "dev-box", pid: 200, start: 6000}) // runs nothing

	res := env.Run("ls")
	rows := lsRows(t, env) // s2, s1

	if want := []string{"lost", "running"}; !reflect.DeepEqual(statuses(rows), want) {
		t.Errorf("statuses = %v, want %v", statuses(rows), want)
	}
	if res.Stderr != "" {
		t.Errorf("stderr = %q, want no warning", res.Stderr)
	}
}

func TestComposeLost_ASessionOlderThanTheServicesContainerBelongedToAnEarlierOneAndIsLostEvenWhereItsPIDRunsNow(t *testing.T) {
	env := newEnv(t)
	// The container was created after the Session's last Event: this is the
	// replacement of the one the Session ran in, and it has the same host name.
	c := namedContainer(t, env, "2031-03-04T12:00:00Z")
	c.Process(100, "claude", 5000)
	composeEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: "dev-box", pid: 100, start: 5000})
	composeEvent(t, env, "s2", "2031-03-04T13:00:00Z", "PreToolUse", proc{host: "dev-box", pid: 100, start: 5000})

	rows := lsRows(t, env) // s2, s1

	if want := []string{"running", "lost"}; !reflect.DeepEqual(statuses(rows), want) {
		t.Errorf("statuses = %v, want %v", statuses(rows), want)
	}
}

func TestComposeLost_AHostNameThatIsNotTheServicesContainersIsNeverJudgedAgainstIt(t *testing.T) {
	env := newEnv(t)
	c := namedContainer(t, env, "2031-03-04T09:00:00Z")
	// Another machine, or another container, wrote this Event; the service's
	// container has no such PID and must not be held against it.
	composeEvent(t, env, "s1", "2031-03-04T10:00:00Z", "Stop", proc{host: "some-other-box", pid: 100, start: 5000})
	before := len(dockerCalls(env, "exec"))

	res := env.Run("ls")

	if !strings.Contains(res.Stdout, "waiting_input") || strings.Contains(res.Stdout, "lost") {
		t.Errorf("stdout = %q, want the status of the Events", res.Stdout)
	}
	if got := len(dockerCalls(env, "exec")) - before; got != 0 {
		t.Errorf("docker exec calls = %d, want none (container %s)", got, c.ID())
	}
}

func TestComposeLost_AHostNameOfItsOwnWhileTheServiceHasNoContainerIsNotJudged(t *testing.T) {
	env := newEnv(t)
	c := namedContainer(t, env, "2031-03-04T09:00:00Z")
	composeEvent(t, env, "s1", "2031-03-04T10:00:00Z", "Stop", proc{host: "dev-box", pid: 100, start: 5000})
	c.Remove()

	res := env.Run("ls")

	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "waiting_input") {
		t.Errorf("exit code = %d, stdout=%q, want the status of the Events", res.ExitCode, res.Stdout)
	}
}

func TestComposeWatch_ALostComposeSessionIsNotifiedLikeAnyOtherWithAtMostOneExecPerContainerPerLook(t *testing.T) {
	env := newEnv(t)
	_, c := composeLostProject(t, env)
	if res := env.Run("new", "app", "fix-login", "--agent", "claude"); res.ExitCode != 0 {
		t.Fatalf("new: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	for _, pid := range []int{100, 101, 102} {
		c.Process(pid, "claude", uint64(5000+pid))
	}
	composeEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: aaaa, pid: 100, start: 5100})
	composeEvent(t, env, "s2", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: aaaa, pid: 101, start: 5101})
	composeEvent(t, env, "s3", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: aaaa, pid: 102, start: 5102})
	startWatch(t, env)

	c.KillProcess(100)
	c.KillProcess(101)
	notifications(t, env, 2)

	// A few more looks, in which the container still holds three Sessions.
	looks := len(dockerCalls(env, "ps"))
	deadline := time.Now().Add(15 * time.Second)
	for len(dockerCalls(env, "ps")) < looks+3 {
		if time.Now().After(deadline) {
			t.Fatal("watch did not look three more times")
		}
		time.Sleep(5 * time.Millisecond)
	}
	want := [][]string{
		{"-a", "baton", "Baton: session lost", "app/fix-login (claude)"},
		{"-a", "baton", "Baton: session lost", "app/fix-login (claude)"},
	}
	if got := othersAfterSentinel(t, env); !reflect.DeepEqual(got, want) {
		t.Errorf("notify-send calls = %q, want %q: the lost Sessions once each, the running one not", got, want)
	}
	if execs, looks := len(dockerCalls(env, "exec")), len(dockerCalls(env, "ps")); execs == 0 || execs > looks {
		t.Errorf("docker exec calls = %d for %d docker ps calls, want at least one and at most one per look", execs, looks)
	}
}

// appTask is the Task of the only Session `baton ls` shows, written as `baton
// handoff` takes it.
func appTask(t *testing.T, env *batontest.Env) string {
	t.Helper()
	return "app/" + firstTask(t, env)
}

func TestComposeHandoff_WaitsForTheComposeSessionAndContinuesWhenItsAgentIsFoundGoneFromTheRunningContainer(t *testing.T) {
	env := newEnv(t)
	_, c := composeLostProject(t, env)
	c.Process(100, "claude", 5000)
	composeEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: aaaa, pid: 100, start: 5000})
	task := appTask(t, env)

	p := env.Start("handoff", task, "--to", "claude")
	p.WaitStdout("waiting for the claude session s1 of " + task + " (profile app.claude, running)")
	if len(env.Calls("guake")) != 0 {
		t.Fatal("a tab was opened while the Agent ran")
	}

	c.KillProcess(100) // the Agent dies; the container keeps running
	res := p.Wait()

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	handoff := env.ReadFile(".baton/projects/app/handoffs/" + strings.TrimPrefix(task, "app/") + "/1.md")
	if !strings.Contains(handoff, "session s1 (lost)") {
		t.Errorf("handoff = %q, want the Session shown as lost", handoff)
	}
	line := typedLine(t, env)
	if !strings.Contains(line, " exec -w /workspace app env ") || !strings.Contains(line, "continue the Task from Handoff /baton/project/handoffs/") {
		t.Errorf("line = %q, want a compose exec that names the Handoff as the container sees it", line)
	}
}

func TestComposeHandoff_FromAHostSessionOfTheProjectToAComposeProfileAndFromOneComposeProfileToAnother(t *testing.T) {
	env := newEnv(t)
	repo, c := composeLostProject(t, env)
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
	commitFile(t, env, repo, "README.md", "hello\n", "first commit")
	c.MkdirAll("/workspace")
	// A Claude started by hand on the host, in the main checkout.
	hostEvent(t, env, "h1", "2031-03-04T10:00:00Z", "SessionStart", repo, proc{})
	hostEvent(t, env, "h1", "2031-03-04T10:05:00Z", "SessionEnd", repo, proc{})
	task := appTask(t, env)

	toCompose := env.Run("handoff", task, "--to", "claude@app.main")
	if toCompose.ExitCode != 0 {
		t.Fatalf("host to compose: exit code = %d, stderr=%q", toCompose.ExitCode, toCompose.Stderr)
	}
	first := typedLine(t, env)
	if !strings.Contains(first, " exec -w /workspace app env ") || !strings.Contains(first, "-u CLAUDE_CONFIG_DIR claude 'continue the Task from Handoff /baton/project/handoffs/") {
		t.Errorf("line = %q, want Claude in the container under app.main, told about the Handoff by its container path", first)
	}

	// The compose Session reports, runs, and ends; the Task goes on in the
	// other Profile of the container.
	composeEvent(t, env, "c1", "2031-03-04T11:00:00Z", "SessionStart", proc{})
	composeEvent(t, env, "c1", "2031-03-04T11:05:00Z", "SessionEnd", proc{})
	toOther := env.Run("handoff", task, "--to", "claude@app.volume")
	if toOther.ExitCode != 0 {
		t.Fatalf("compose to compose: exit code = %d, stderr=%q", toOther.ExitCode, toOther.Stderr)
	}
	calls := guakeArgs(env)
	if len(calls) != 2 || !strings.Contains(calls[1][1], "CLAUDE_CONFIG_DIR=/state/.claude claude 'continue the Task from Handoff /baton/project/handoffs/") {
		t.Errorf("guake calls = %q, want a second tab with the Profile at /state/.claude", calls)
	}
}

func TestComposeHandoff_ToAWorkingCopyOtherThanTheMainCheckoutIsRefusedBeforeAnythingIsWrittenOrStarted(t *testing.T) {
	env := newEnv(t)
	repo, _ := composeLostProject(t, env)
	commitFile(t, env, repo, "README.md", "hello\n", "first commit")
	worktree := addWorktree(t, env, repo, "feature")
	// A Claude started by hand on the host, in the worktree.
	hostEvent(t, env, "h1", "2031-03-04T10:00:00Z", "SessionStart", worktree, proc{})
	hostEvent(t, env, "h1", "2031-03-04T10:05:00Z", "SessionEnd", worktree, proc{})

	res := env.Run("handoff", appTask(t, env), "--to", "claude")

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "whose Sessions run only in the main checkout "+repo+", not in the worktree "+worktree) {
		t.Errorf("exit code = %d, stderr=%q, want a refusal that names the main checkout and the worktree", res.ExitCode, res.Stderr)
	}
	if env.Exists(".baton/projects/app/handoffs") || len(env.Calls("guake")) != 0 {
		t.Error("a Handoff was written or a tab opened")
	}
	for _, call := range composeDockerCalls(env) {
		if startsOrChangesAService(call) {
			t.Errorf("docker call %q starts or changes a service", call)
		}
	}
}

// composeTask makes the current Task of Project app with a commit and a Claude
// Session that has ended, and returns the Task's id and the root of a container
// in which the Project's Baton directory is mounted.
func composeTask(t *testing.T, env *batontest.Env) (task, root string) {
	t.Helper()
	repo, _ := composeLostProject(t, env)
	commitFile(t, env, repo, "README.md", "hello\n", "first commit")
	if res := env.Run("new", "app", "--agent", "claude"); res.ExitCode != 0 {
		t.Fatalf("new: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	tasks := env.ListDir(env.Path(".baton", "projects", "app", "tasks"))
	if len(tasks) != 1 {
		t.Fatalf("tasks = %v, want one", tasks)
	}
	return strings.TrimSuffix(tasks[0], ".toml"), containerRoot(t, env, "app")
}

func TestComposeHandoffSave_InTheContainerFindsTheTaskThroughTheMountAndWritesTheDraftThere(t *testing.T) {
	env := newEnv(t)
	task, root := composeTask(t, env)
	draft := env.Path(".baton", "projects", "app", "handoffs", task, "draft.md")

	// The Agent in the container: its directory is /workspace, which is not a
	// path of the host; the Task is found through the mount.
	res := saveInContainer(env, root, "## Goal\n\nMake login work.\n")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got, want := env.ReadFile(draft), "## Goal\n\nMake login work.\n"; got != want {
		t.Errorf("draft = %q, want %q", got, want)
	}
	if want := filepath.Join(root, "baton", "project", "handoffs", task, "draft.md"); !strings.Contains(res.Stdout, want) {
		t.Errorf("stdout = %q, want the draft named as the container sees it: %s", res.Stdout, want)
	}
}

func TestComposeHandoffSave_InTheContainerTheReceiverFillsTheHandoffThatHasNoNarrative(t *testing.T) {
	env := newEnv(t)
	task, root := composeTask(t, env)
	composeEvent(t, env, "c1", "2031-03-04T11:00:00Z", "SessionStart", proc{})
	composeEvent(t, env, "c1", "2031-03-04T11:05:00Z", "SessionEnd", proc{})
	if res := env.Run("handoff", "app/"+task, "--to", "claude"); res.ExitCode != 0 {
		t.Fatalf("handoff: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	handoff := env.Path(".baton", "projects", "app", "handoffs", task, "1.md")
	if !strings.Contains(env.ReadFile(handoff), "narrative: missing\n") {
		t.Fatalf("handoff = %q, want narrative: missing", env.ReadFile(handoff))
	}

	res := saveInContainer(env, root, "## Next step\n\nRun the tests.\n")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	got := env.ReadFile(handoff)
	if !strings.Contains(got, "narrative: receiving\n") || !strings.HasSuffix(got, "\n## Narrative\n\n## Next step\n\nRun the tests.\n") {
		t.Errorf("handoff not filled by the receiver:\n%s", got)
	}
}

func TestComposeWatch_ASessionInAContainerThatAppearsAfterTheWatcherFirstFailedToPlaceOneIsPlacedInItsTask(t *testing.T) {
	env := newEnv(t)
	repo, c := composeLostProject(t, env)
	if res := env.Run("new", "app", "fix-login", "--agent", "claude"); res.ExitCode != 0 {
		t.Fatalf("new: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	c.Process(100, "claude", 5000)
	composeEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: aaaa, container: c.ID(), pid: 100, start: 5000})
	c.Remove() // the service's container is gone: a Session in it cannot be placed
	startWatch(t, env)
	composeEvent(t, env, "s1", "2031-03-04T10:00:05Z", "PermissionRequest", proc{host: aaaa, container: c.ID(), pid: 100, start: 5000})
	notifications(t, env, 1)

	// `docker compose up -d` makes a container again, with the checkout in
	// it, and the Agent asks for approval there.
	fresh := env.AddContainer(batontest.ContainerSpec{
		Service: "app", Running: true,
		Binds:   map[string]string{repo: "/workspace", env.Path(".baton/projects/app"): "/baton/project"},
		BindsRO: map[string]string{env.Path(".baton/bin"): "/baton/bin"},
	})
	fresh.Process(100, "claude", 5000)
	composeEvent(t, env, "s2", "2031-03-04T10:05:00Z", "PermissionRequest", proc{host: "bbbbbbbbbbbb", container: fresh.ID(), pid: 100, start: 5000})

	got := notifyArgs(notifications(t, env, 2))
	if want := []string{"-a", "baton", "Baton: waiting for approval", "app/fix-login (claude)"}; !reflect.DeepEqual(got[1], want) {
		t.Errorf("notify-send calls = %q, want the second to name the Task: the container is looked at again, not remembered from before it existed", got)
	}
}
