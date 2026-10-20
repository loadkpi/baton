package test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/loadkpi/baton/internal/batontest"
)

// proc is what an Event says about the Agent's process.
type proc struct {
	host  string
	pid   int
	start uint64
	// fallback marks a PID that is only the hook's parent, not a recognized Agent.
	fallback bool
	// container is the container id the hook found for itself; empty leaves
	// the field out, as the hook does where it could not tell.
	container string
}

// writeProcEvent appends an Event that carries the given process to the Event
// file at path (relative to HOME), the way a hook would have written it.
func writeProcEvent(t *testing.T, env *batontest.Env, path, rt, profile, sessionID, ts, event, cwd string, p proc) {
	t.Helper()
	writeProcEventPayload(t, env, path, rt, profile, sessionID, ts, event, cwd, p, map[string]any{})
}

// writeProcEventPayload is writeProcEvent with the payload the Agent sent.
func writeProcEventPayload(t *testing.T, env *batontest.Env, path, rt, profile, sessionID, ts, event, cwd string, p proc, payload map[string]any) {
	t.Helper()
	doc := map[string]any{
		"v": 1, "ts": ts, "agent": "claude", "profile": profile, "runtime": rt,
		"host": p.host, "agent_pid": p.pid, "agent_start": p.start, "session_id": sessionID,
		"event": event, "cwd": cwd, "payload": payload,
	}
	if p.fallback {
		doc["agent_pid_fallback"] = true
	}
	if p.container != "" {
		doc["container"] = p.container
	}
	line, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	existing := ""
	if env.Exists(path) {
		existing = env.ReadFile(path)
	}
	env.WriteFile(path, existing+string(line)+"\n")
}

// hostEvent appends an Event of a host Claude Session.
func hostEvent(t *testing.T, env *batontest.Env, sessionID, ts, event, cwd string, p proc) {
	t.Helper()
	writeProcEvent(t, env, ".baton/sessions/claude-"+sessionID+".jsonl", "host", "claude-personal", sessionID, ts, event, cwd, p)
}

// thisHost is the name this machine gives itself, which is what a hook on the
// host records.
func thisHost(t *testing.T) string {
	t.Helper()
	h, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// deadPID starts a short process, waits for it and returns its PID: a PID that
// runs nothing.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

// livePID starts a process that runs until the test ends and returns its PID
// and its start time as /proc has it (clock ticks since boot).
func livePID(t *testing.T) (pid int, start uint64) {
	t.Helper()
	cmd, start := sleeper(t)
	return cmd.Process.Pid, start
}

// sleeper starts a process that runs until the test ends, or until the test
// kills it, and returns it with its start time as /proc has it.
func sleeper(t *testing.T) (*exec.Cmd, uint64) {
	t.Helper()
	cmd := exec.Command("sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	_, start := readStat(t, cmd.Process.Pid)
	return cmd, start
}

// readStat returns the state (field 3) and the start time (field 22) of a
// process as /proc has them.
func readStat(t *testing.T, pid int) (state string, start uint64) {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		t.Fatal(err)
	}
	stat := string(data)
	fields := strings.Fields(stat[strings.LastIndexByte(stat, ')')+1:])
	start, err = strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return fields[0], start
}

// zombiePID makes a zombie: a process that was killed (here, it finished) and
// whose parent does not reap it. The shell starts a short `sleep` in the
// background and then becomes a long `sleep`, which never waits for children.
// It returns the zombie's PID and start time.
func zombiePID(t *testing.T) (pid int, start uint64) {
	t.Helper()
	parent := exec.Command("sh", "-c", "sleep 0.1 & exec sleep 300")
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = parent.Process.Kill()
		_ = parent.Wait()
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if child := childOf(t, parent.Process.Pid); child != 0 {
			if state, start := readStat(t, child); state == "Z" {
				return child, start
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no zombie child appeared")
	return 0, 0
}

// childOf returns the PID of a child of parent, or 0 when it has none (yet).
func childOf(t *testing.T, parent int) int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue // it exited meanwhile
		}
		stat := string(data)
		fields := strings.Fields(stat[strings.LastIndexByte(stat, ')')+1:])
		if ppid, err := strconv.Atoi(fields[1]); err == nil && ppid == parent {
			return pid
		}
	}
	return 0
}

func TestLost_AHostSessionWhoseAgentProcessIsGoneShowsAsLost(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", repo, proc{host: thisHost(t), pid: deadPID(t), start: 4242})

	rows := lsRows(t, env)

	if len(rows) != 1 || rows[0][6] != "lost" {
		t.Errorf("rows = %v, want one Session with status lost", rows)
	}
}

func TestLost_AHostSessionWhoseAgentProcessRunsStaysWithTheStatusOfItsEvents(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	pid, start := livePID(t)
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", repo, proc{host: thisHost(t), pid: pid, start: start})

	rows := lsRows(t, env)

	if len(rows) != 1 || rows[0][6] != "running" {
		t.Errorf("rows = %v, want one Session with status running", rows)
	}
}

func TestLost_AHostSessionWhoseAgentIsAZombieShowsAsLost(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	pid, start := zombiePID(t) // killed, but its parent has not reaped it: /proc/<pid>/stat still exists
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", repo, proc{host: thisHost(t), pid: pid, start: start})

	rows := lsRows(t, env)

	if len(rows) != 1 || rows[0][6] != "lost" {
		t.Errorf("rows = %v, want one Session with status lost", rows)
	}
}

func TestLost_AReusedPIDIsNotTheAgent(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	pid, start := livePID(t) // some other process runs under the recorded PID now
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "Stop", repo, proc{host: thisHost(t), pid: pid, start: start + 1})

	rows := lsRows(t, env)

	if len(rows) != 1 || rows[0][6] != "lost" {
		t.Errorf("rows = %v, want one Session with status lost", rows)
	}
}

func TestLost_ASessionThatEndedIsEndedWhateverBecameOfItsProcess(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	p := proc{host: thisHost(t), pid: deadPID(t), start: 4242}
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", repo, p)
	hostEvent(t, env, "s1", "2031-03-04T10:05:00Z", "SessionEnd", repo, p)

	rows := lsRows(t, env)

	if len(rows) != 1 || rows[0][6] != "ended" {
		t.Errorf("rows = %v, want one Session with status ended", rows)
	}
}

func TestLost_AnEventFromAnotherMachineIsNotJudgedByThisMachinesProcesses(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	// The PID is dead here, but the Event says it was somewhere else.
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "Stop", repo, proc{host: thisHost(t) + "-other", pid: deadPID(t), start: 4242})

	rows := lsRows(t, env)

	if len(rows) != 1 || rows[0][6] != "waiting_input" {
		t.Errorf("rows = %v, want one Session with status waiting_input", rows)
	}
}

func TestLost_APIDThatIsOnlyTheHooksParentSaysNothingAboutTheAgent(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "Stop", repo, proc{host: thisHost(t), pid: deadPID(t), start: 4242, fallback: true})

	rows := lsRows(t, env)

	if len(rows) != 1 || rows[0][6] != "waiting_input" {
		t.Errorf("rows = %v, want one Session with status waiting_input", rows)
	}
}

func TestLost_AnEventWithoutAProcessIsNotJudged(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "Stop", repo, proc{host: thisHost(t), pid: 0, start: 0})

	rows := lsRows(t, env)

	if len(rows) != 1 || rows[0][6] != "waiting_input" {
		t.Errorf("rows = %v, want one Session with status waiting_input", rows)
	}
}

func TestLost_TheNewestEventDecidesWhichProcessIsChecked(t *testing.T) {
	// A conversation resumed in a new process reports that process from then on.
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	pid, start := livePID(t)
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", repo, proc{host: thisHost(t), pid: deadPID(t), start: 4242})
	hostEvent(t, env, "s1", "2031-03-04T11:00:00Z", "PreToolUse", repo, proc{host: thisHost(t), pid: pid, start: start})

	rows := lsRows(t, env)

	if len(rows) != 1 || rows[0][6] != "running" {
		t.Errorf("rows = %v, want one Session with status running", rows)
	}
}

func TestLost_IsComputedOnReadAndNothingIsWrittenToTheEventFile(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "my-app")
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", repo, proc{host: thisHost(t), pid: deadPID(t), start: 4242})
	before := env.ReadFile(".baton/sessions/claude-s1.jsonl")
	modTime := env.ModTime(".baton/sessions/claude-s1.jsonl")

	lsRows(t, env)
	lsRows(t, env)

	if after := env.ReadFile(".baton/sessions/claude-s1.jsonl"); after != before {
		t.Errorf("Event file changed:\nbefore %q\nafter  %q", before, after)
	}
	if got := env.ModTime(".baton/sessions/claude-s1.jsonl"); !got.Equal(modTime) {
		t.Errorf("Event file modified at %v, was %v", got, modTime)
	}
	if got := env.ListDir(".baton/sessions"); len(got) != 1 {
		t.Errorf("sessions directory = %v, want only the one Event file", got)
	}
}

func TestLost_ALostSessionNoLongerBlocksNewInItsWorkingCopy(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "my-app")
	running, start := livePID(t)
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", repo, proc{host: thisHost(t), pid: running, start: start})

	blocked := env.Run("new", repo, "--agent", "claude")
	// The Agent dies: from now on the recorded start time is not the PID's.
	hostEvent(t, env, "s1", "2031-03-04T10:01:00Z", "PreToolUse", repo, proc{host: thisHost(t), pid: running, start: start + 1})
	unblocked := env.Run("new", repo, "--agent", "claude")

	if blocked.ExitCode != 1 || !strings.Contains(blocked.Stderr, "already has an active session") {
		t.Errorf("a running Session did not block: exit code = %d, stderr=%q", blocked.ExitCode, blocked.Stderr)
	}
	if unblocked.ExitCode != 0 {
		t.Errorf("a lost Session still blocks: exit code = %d, stderr=%q", unblocked.ExitCode, unblocked.Stderr)
	}
}

// containerID is the 64-character id of a container whose first 12 characters,
// which are the host name inside it, are prefix.
func containerID(prefix string) string {
	return prefix + strings.Repeat("0", 64-len(prefix))
}

// fakeDocker is the docker stub of the tests of the sandbox: a directory of
// fixtures that `docker ps` and `docker exec` answer from.
//
//	running            the ids of the running containers, one per line
//	fail-ps            if present, `docker ps` fails
//	<id>/proc/<pid>/stat   the processes of a container <id>
//	<id>/fail-exec     if present, `docker exec` into <id> fails
//
// `docker exec` runs the command it is given with `/proc/` pointing into the
// container's fixtures, so the command Baton wrote is what is tested. Every
// other verb succeeds and prints nothing.
type fakeDocker struct {
	t   *testing.T
	env *batontest.Env
	dir string
}

func newFakeDocker(t *testing.T, env *batontest.Env, running ...string) *fakeDocker {
	t.Helper()
	d := &fakeDocker{t: t, env: env, dir: env.Path("docker-fixtures")}
	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	d.setRunning(running...)
	env.Stub("docker", batontest.WithScript(fmt.Sprintf(`FIX=%s
case "$1" in
ps)
  if [ -e "$FIX/fail-ps" ]; then echo "Cannot connect to the Docker daemon at unix:///var/run/docker.sock" >&2; exit 1; fi
  cat "$FIX/running"
  ;;
exec)
  id=$2
  if [ ! -d "$FIX/$id" ]; then echo "Error response from daemon: No such container: $id" >&2; exit 1; fi
  if [ -e "$FIX/$id/fail-exec" ]; then echo "Error response from daemon: container is paused" >&2; exit 1; fi
  shift 2
  script=$(printf '%%s' "$3" | sed "s#/proc/#$FIX/$id/proc/#g")
  shift 3
  exec sh -c "$script" "$@"
  ;;
esac
exit 0
`, "'"+d.dir+"'")))
	return d
}

func (d *fakeDocker) setRunning(ids ...string) {
	d.t.Helper()
	d.env.WriteFile("docker-fixtures/running", strings.Join(ids, "\n")+"\n")
	for _, id := range ids {
		if err := os.MkdirAll(filepath.Join(d.dir, id, "proc"), 0o755); err != nil {
			d.t.Fatal(err)
		}
	}
}

// process makes pid run in the container with the start time.
func (d *fakeDocker) process(id string, pid int, comm string, start uint64) {
	d.t.Helper()
	d.env.WriteFile(fmt.Sprintf("docker-fixtures/%s/proc/%d/stat", id, pid), statLine(pid, comm, start))
}

// processInState makes pid exist in the container in the given state.
func (d *fakeDocker) processInState(id string, pid int, comm, state string, start uint64) {
	d.t.Helper()
	d.env.WriteFile(fmt.Sprintf("docker-fixtures/%s/proc/%d/stat", id, pid), stateLine(pid, comm, state, start))
}

// statLine is /proc/<pid>/stat as the kernel prints it, with the start time in
// field 22.
func statLine(pid int, comm string, start uint64) string {
	return stateLine(pid, comm, "S", start)
}

// stateLine is statLine for a process in the given state (field 3).
func stateLine(pid int, comm, state string, start uint64) string {
	return fmt.Sprintf("%d (%s) %s 1 %d %d 0 -1 4194560 100 0 0 0 1 1 0 0 20 0 1 0 %d 1000000 500 18446744073709551615\n", pid, comm, state, pid, pid, start)
}

// calls returns the arguments of the docker calls whose first argument is verb.
func (d *fakeDocker) calls(verb string) [][]string {
	return dockerCalls(d.env, verb)
}

// sandboxEvent appends an Event of a sandbox Claude Session of Project app, as
// the hook in a container with the given id would have written it.
func sandboxEvent(t *testing.T, env *batontest.Env, sessionID, ts, event string, p proc) {
	t.Helper()
	writeProcEvent(t, env, ".baton/projects/app/sessions/claude-"+sessionID+".jsonl", "sandbox", "app.claude", sessionID, ts, event, "/workspace", p)
}

// statuses is the status column of rows.
func statuses(rows [][]string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r[6]
	}
	return out
}

func TestLost_ASandboxSessionWhoseContainerIsGoneShowsAsLost(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	newOK(t, env)
	docker := newFakeDocker(t, env, containerID("bbbbbbbbbbbb")) // another container runs
	sandboxEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: "aaaaaaaaaaaa", pid: 100, start: 5000})
	execBefore := len(docker.calls("exec"))

	rows := lsRows(t, env)

	if !reflect.DeepEqual(statuses(rows), []string{"lost"}) {
		t.Errorf("rows = %v, want one Session with status lost", rows)
	}
	if got := len(docker.calls("exec")) - execBefore; got != 0 {
		t.Errorf("docker exec calls = %d, want none: there is no container to ask", got)
	}
}

func TestLost_ASandboxSessionIsLostWhenItsPIDIsGoneFromARunningContainerOrIsAnotherProcessNow(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	newOK(t, env)
	id := containerID("aaaaaaaaaaaa")
	docker := newFakeDocker(t, env, id)
	docker.process(id, 100, "claude", 5000) // s1: the Agent
	docker.process(id, 300, "bash", 9999)   // s3: the PID was reused by another process
	// s2 recorded PID 200, which runs nothing.
	sandboxEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: "aaaaaaaaaaaa", pid: 100, start: 5000})
	sandboxEvent(t, env, "s2", "2031-03-04T10:01:00Z", "PreToolUse", proc{host: "aaaaaaaaaaaa", pid: 200, start: 6000})
	sandboxEvent(t, env, "s3", "2031-03-04T10:02:00Z", "Stop", proc{host: "aaaaaaaaaaaa", pid: 300, start: 6000})

	rows := lsRows(t, env) // the most recent first: s3, s2, s1

	if want := []string{"lost", "lost", "running"}; !reflect.DeepEqual(statuses(rows), want) {
		t.Errorf("statuses = %v, want %v", statuses(rows), want)
	}
}

func TestLost_ASandboxSessionWhoseAgentIsAZombieOrDeadIsLost(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	newOK(t, env)
	id := containerID("aaaaaaaaaaaa")
	docker := newFakeDocker(t, env, id)
	docker.process(id, 100, "claude", 5000)             // s1: the Agent runs
	docker.processInState(id, 200, "claude", "Z", 6000) // s2: killed, not reaped by its parent
	docker.processInState(id, 300, "claude", "X", 7000) // s3: dead
	sandboxEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: "aaaaaaaaaaaa", pid: 100, start: 5000})
	sandboxEvent(t, env, "s2", "2031-03-04T10:01:00Z", "PreToolUse", proc{host: "aaaaaaaaaaaa", pid: 200, start: 6000})
	sandboxEvent(t, env, "s3", "2031-03-04T10:02:00Z", "PreToolUse", proc{host: "aaaaaaaaaaaa", pid: 300, start: 7000})

	rows := lsRows(t, env) // the most recent first: s3, s2, s1

	if want := []string{"lost", "lost", "running"}; !reflect.DeepEqual(statuses(rows), want) {
		t.Errorf("statuses = %v, want %v", statuses(rows), want)
	}
}

func TestLost_OneDockerPsAndOneDockerExecPerRunningContainerWhateverTheNumberOfSessions(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	newOK(t, env)
	a, b, gone := containerID("aaaaaaaaaaaa"), containerID("bbbbbbbbbbbb"), "cccccccccccc"
	docker := newFakeDocker(t, env, a, b)
	docker.process(a, 100, "claude", 5000)
	docker.process(a, 101, "claude", 5001)
	docker.process(b, 100, "claude", 7000)
	sandboxEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: "aaaaaaaaaaaa", pid: 100, start: 5000})
	sandboxEvent(t, env, "s2", "2031-03-04T10:01:00Z", "PreToolUse", proc{host: "aaaaaaaaaaaa", pid: 101, start: 5001})
	sandboxEvent(t, env, "s3", "2031-03-04T10:02:00Z", "PreToolUse", proc{host: "bbbbbbbbbbbb", pid: 100, start: 7000})
	sandboxEvent(t, env, "s4", "2031-03-04T10:03:00Z", "PreToolUse", proc{host: gone, pid: 100, start: 7000})
	sandboxEvent(t, env, "s5", "2031-03-04T10:04:00Z", "SessionEnd", proc{host: gone, pid: 100, start: 7000})
	psBefore, execBefore := len(docker.calls("ps")), len(docker.calls("exec"))

	rows := lsRows(t, env) // s5, s4, s3, s2, s1

	if want := []string{"ended", "lost", "running", "running", "running"}; !reflect.DeepEqual(statuses(rows), want) {
		t.Errorf("statuses = %v, want %v", statuses(rows), want)
	}
	if got := len(docker.calls("ps")) - psBefore; got != 1 {
		t.Errorf("docker ps calls = %d, want 1", got)
	}
	execs := docker.calls("exec")[execBefore:]
	if len(execs) != 2 {
		t.Fatalf("docker exec calls = %q, want one per running container", execs)
	}
	// One call per container, with all the PIDs of its Sessions after the script.
	for i, want := range [][]string{{a, "100", "101"}, {b, "100"}} {
		args := execs[i]
		got := append([]string{args[1]}, args[6:]...)
		if args[0] != "exec" || args[2] != "sh" || args[3] != "-c" || !reflect.DeepEqual(got, want) {
			t.Errorf("docker exec call %d = %q, want container %s and PIDs %v after the script", i, args, want[0], want[1:])
		}
	}
}

func TestLost_NothingIsAskedOfDockerAboutSessionsThatEndedOrThatRunOnTheHost(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	newOK(t, env)
	newFakeDocker(t, env)
	repo := newRepo(t, env, "my-app")
	sandboxEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionEnd", proc{host: "aaaaaaaaaaaa", pid: 100, start: 5000})
	pid, start := livePID(t)
	hostEvent(t, env, "h1", "2031-03-04T10:01:00Z", "PreToolUse", repo, proc{host: thisHost(t), pid: pid, start: start})
	before := len(env.Calls("docker"))

	lsRows(t, env)

	if got := env.Calls("docker")[before:]; len(got) != 0 {
		t.Errorf("docker was called: %v", got)
	}
}

func TestLost_ASandboxSessionWhoseHostNameIsNotAContainerIDIsNotJudged(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	newOK(t, env)
	docker := newFakeDocker(t, env)
	sandboxEvent(t, env, "s1", "2031-03-04T10:00:00Z", "Stop", proc{host: "my-dev-box", pid: 100, start: 5000})
	before := len(docker.calls("ps"))

	rows := lsRows(t, env)

	if !reflect.DeepEqual(statuses(rows), []string{"waiting_input"}) {
		t.Errorf("rows = %v, want one Session with status waiting_input", rows)
	}
	if got := len(docker.calls("ps")) - before; got != 0 {
		t.Errorf("docker ps calls = %d, want none", got)
	}
}

func TestLost_WhenDockerFailsSessionsKeepTheStatusOfTheirEventsAndOneWarningSaysWhy(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	newOK(t, env)
	id := containerID("aaaaaaaaaaaa")
	newFakeDocker(t, env, id)
	env.WriteFile("docker-fixtures/fail-ps", "")
	sandboxEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: "aaaaaaaaaaaa", pid: 100, start: 5000})
	sandboxEvent(t, env, "s2", "2031-03-04T10:01:00Z", "Stop", proc{host: "bbbbbbbbbbbb", pid: 100, start: 5000})

	res := env.Run("ls")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "running") || !strings.Contains(res.Stdout, "waiting_input") || strings.Contains(res.Stdout, "lost") {
		t.Errorf("stdout = %q, want the statuses of the events and no lost", res.Stdout)
	}
	lines := strings.Split(strings.TrimRight(res.Stderr, "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "baton: warning: cannot tell whether sandbox sessions are still running") ||
		!strings.Contains(lines[0], "Cannot connect to the Docker daemon") {
		t.Errorf("stderr = %q, want exactly one warning that names the failure of docker", res.Stderr)
	}
}

func TestLost_WhenDockerExecFailsForOneContainerOnlyItsSessionsKeepTheStatusOfTheirEvents(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	newOK(t, env)
	a, b := containerID("aaaaaaaaaaaa"), containerID("bbbbbbbbbbbb")
	newFakeDocker(t, env, a, b)
	env.WriteFile("docker-fixtures/"+b+"/fail-exec", "")                                                                // paused, say
	sandboxEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: "aaaaaaaaaaaa", pid: 100, start: 5000}) // not running there
	sandboxEvent(t, env, "s2", "2031-03-04T10:01:00Z", "Stop", proc{host: "bbbbbbbbbbbb", pid: 100, start: 5000})

	res := env.Run("ls")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	rows := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")[1:]
	if len(rows) != 2 || !strings.HasSuffix(rows[0], "waiting_input") || !strings.HasSuffix(rows[1], "lost") {
		t.Errorf("rows = %q, want s2 as waiting_input and s1 as lost", rows)
	}
	lines := strings.Split(strings.TrimRight(res.Stderr, "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "baton: warning: cannot tell whether sandbox sessions are still running") ||
		!strings.Contains(lines[0], "container is paused") {
		t.Errorf("stderr = %q, want exactly one warning that names the failure of docker exec", res.Stderr)
	}
}

func TestLost_ALostSandboxSessionNoLongerBlocksNewInItsWorkingCopy(t *testing.T) {
	env := newEnv(t)
	launchProject(t, env)
	newOK(t, env)
	id := containerID("aaaaaaaaaaaa")
	docker := newFakeDocker(t, env, id)
	docker.process(id, 100, "claude", 5000)
	sandboxEvent(t, env, "s1", "2031-03-04T10:00:00Z", "PreToolUse", proc{host: "aaaaaaaaaaaa", pid: 100, start: 5000})

	blocked := env.Run("new", "app", "--agent", "claude")
	docker.setRunning() // the container is gone
	unblocked := env.Run("new", "app", "--agent", "claude")

	if blocked.ExitCode != 1 || !strings.Contains(blocked.Stderr, "already has an active session") {
		t.Errorf("a running Session did not block: exit code = %d, stderr=%q", blocked.ExitCode, blocked.Stderr)
	}
	if unblocked.ExitCode != 0 {
		t.Errorf("a lost Session still blocks: exit code = %d, stderr=%q", unblocked.ExitCode, unblocked.Stderr)
	}
}
