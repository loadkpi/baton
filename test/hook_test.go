package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/loadkpi/baton/internal/batontest"
)

// payload returns the fixture of an Event (testdata/claude-hooks, see its
// README) with the session id and the directory of the test.
func payload(t *testing.T, event, sessionID, cwd string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "claude-hooks", event+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["session_id"] = sessionID
	doc["cwd"] = cwd
	out, err := json.MarshalIndent(doc, "", "  ") // like Claude, not on one line
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// hookArgs are the arguments sync writes into the hooks of a host Profile.
func hookArgs(profile string) []string {
	return []string{"hook", "claude", "--profile", profile, "--runtime", "host"}
}

// runHook plays Claude calling the hook of a Profile for one Event.
func runHook(t *testing.T, env *batontest.Env, profile, event, sessionID, cwd string) batontest.Result {
	t.Helper()
	return env.RunWithStdin(payload(t, event, sessionID, cwd), hookArgs(profile)...)
}

// eventLines reads an Event file: one JSON document per line.
func eventLines(t *testing.T, env *batontest.Env, path string) []map[string]any {
	t.Helper()
	text := env.ReadFile(path)
	if !strings.HasSuffix(text, "\n") {
		t.Fatalf("%s does not end with a newline: %q", path, text)
	}
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		var doc map[string]any
		if err := json.Unmarshal([]byte(line), &doc); err != nil {
			t.Fatalf("%s: line %q is not JSON: %v", path, line, err)
		}
		lines = append(lines, doc)
	}
	return lines
}

func TestHook_AppendsOneLineWithTheEventAndTheRawPayloadToTheSessionsFile(t *testing.T) {
	env := newEnv(t)
	cwd := env.Path("work", "app")
	before := time.Now().Add(-time.Second)

	res := runHook(t, env, "claude-personal", "Stop", "abc123", cwd)

	if res.ExitCode != 0 || res.Stdout != "" {
		t.Fatalf("exit code = %d, stdout=%q, stderr=%q, want a silent success", res.ExitCode, res.Stdout, res.Stderr)
	}
	lines := eventLines(t, env, ".baton/sessions/claude-abc123.jsonl")
	if len(lines) != 1 {
		t.Fatalf("%d lines, want 1", len(lines))
	}
	line := lines[0]
	host, _ := os.Hostname()
	for key, want := range map[string]any{
		"v": float64(1), "agent": "claude", "profile": "claude-personal", "runtime": "host",
		"host": host, "session_id": "abc123", "event": "Stop", "cwd": cwd,
	} {
		if !reflect.DeepEqual(line[key], want) {
			t.Errorf("%s = %#v, want %#v", key, line[key], want)
		}
	}
	ts, err := time.Parse(time.RFC3339Nano, line["ts"].(string))
	if err != nil || ts.Before(before) || ts.After(time.Now().Add(time.Second)) {
		t.Errorf("ts = %v (%v), want now", line["ts"], err)
	}
	var wantPayload map[string]any
	if err := json.Unmarshal([]byte(payload(t, "Stop", "abc123", cwd)), &wantPayload); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(line["payload"], wantPayload) {
		t.Errorf("payload = %v, want the Stop payload as it came in", line["payload"])
	}
}

func TestHook_EventsOfOneSessionGoToOneFileInOrderAndOthersToTheirOwn(t *testing.T) {
	env := newEnv(t)
	cwd := env.Path("work", "app")

	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop"} {
		if res := runHook(t, env, "claude-personal", event, "abc123", cwd); res.ExitCode != 0 {
			t.Fatalf("%s: exit code = %d, stderr=%q", event, res.ExitCode, res.Stderr)
		}
	}
	runHook(t, env, "claude-work", "SessionStart", "def456", cwd)

	if got := env.ListDir(".baton/sessions"); !reflect.DeepEqual(got, []string{"claude-abc123.jsonl", "claude-def456.jsonl"}) {
		t.Errorf("sessions = %v, want one file per Session", got)
	}
	var events []string
	for _, line := range eventLines(t, env, ".baton/sessions/claude-abc123.jsonl") {
		events = append(events, line["event"].(string))
	}
	if want := []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop"}; !reflect.DeepEqual(events, want) {
		t.Errorf("events = %v, want %v", events, want)
	}
	if got := eventLines(t, env, ".baton/sessions/claude-def456.jsonl")[0]["profile"]; got != "claude-work" {
		t.Errorf("profile of the second Session = %v", got)
	}
}

func TestHook_EventFilesAreReadableOnlyByTheirOwner(t *testing.T) {
	env := newEnv(t)

	runHook(t, env, "claude-personal", "Stop", "abc123", env.Home)

	if mode := env.Mode(".baton/sessions/claude-abc123.jsonl"); mode != 0o600 {
		t.Errorf("Event file mode = %v, want 0600", mode)
	}
	if mode := env.Mode(".baton/sessions"); mode&0o077 != 0 {
		t.Errorf("sessions directory mode = %v, want it closed to others", mode)
	}
}

func TestHook_KeepsThePayloadAsTheAgentWroteItEvenWhereJSONEncodersEscape(t *testing.T) {
	env := newEnv(t)
	in := `{
  "session_id": "abc123",
  "cwd": "/x",
  "hook_event_name": "PreToolUse",
  "tool_input": {"command": "make && echo \"<done>\" > out.txt"}
}`

	res := env.RunWithStdin(in, hookArgs("claude-personal")...)

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	text := env.ReadFile(".baton/sessions/claude-abc123.jsonl")
	if !strings.Contains(text, `"command":"make && echo \"<done>\" > out.txt"`) {
		t.Errorf("Event line = %q, want the command unescaped and unchanged", text)
	}
	if strings.Count(text, "\n") != 1 {
		t.Errorf("Event file = %q, want exactly one line", text)
	}
}

func TestHook_RefusesWhatItCannotFileAndNeverBlocksTheAgent(t *testing.T) {
	good := `{"session_id": "abc123", "hook_event_name": "Stop", "cwd": "/x"}`
	tests := []struct {
		name  string
		stdin string
		args  []string
	}{
		{"not JSON", "Stop", hookArgs("claude-personal")},
		{"empty input", "", hookArgs("claude-personal")},
		{"no session id", `{"hook_event_name": "Stop"}`, hookArgs("claude-personal")},
		{"a session id that leaves the directory", `{"session_id": "../../x", "hook_event_name": "Stop"}`, hookArgs("claude-personal")},
		{"a session id with a slash", `{"session_id": "a/b", "hook_event_name": "Stop"}`, hookArgs("claude-personal")},
		{"no event name", `{"session_id": "abc123"}`, hookArgs("claude-personal")},
		{"unknown agent", good, []string{"hook", "gemini", "--profile", "p", "--runtime", "host"}},
		{"no profile", good, []string{"hook", "claude", "--runtime", "host"}},
		{"unknown runtime", good, []string{"hook", "claude", "--profile", "p", "--runtime", "moon"}},
		{"no agent", good, []string{"hook"}},
		{"unknown flag", good, []string{"hook", "claude", "--profile", "p", "--runtime", "host", "--x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t)

			res := env.RunWithStdin(tt.stdin, tt.args...)

			// Exit code 2 from a Claude hook blocks the action it is about.
			if res.ExitCode != 1 || res.Stderr == "" || res.Stdout != "" {
				t.Errorf("exit code = %d, stdout=%q, stderr=%q, want 1, nothing on stdout and a message", res.ExitCode, res.Stdout, res.Stderr)
			}
			if env.Exists(".baton/sessions") && len(env.ListDir(".baton/sessions")) != 0 {
				t.Errorf("sessions = %v, want nothing written", env.ListDir(".baton/sessions"))
			}
			if env.Exists("x") {
				t.Error("a file was written outside the sessions directory")
			}
		})
	}
}

func TestHook_CallsNoGit(t *testing.T) {
	env := newEnv(t)
	env.Stub("git")

	res := runHook(t, env, "claude-personal", "SessionStart", "abc123", env.SourceDir)

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if calls := env.Calls("git"); len(calls) != 0 {
		t.Errorf("git was called: %v", calls)
	}
	if env.Exists(".baton/projects") {
		t.Error("the hook registered a Project")
	}
}

// claudeRunsHook makes the claude stub play Claude Code starting a hook: it
// feeds the payload to `baton hook`, directly or through an inner `sh -c`, and
// leaves its own PID and start time (clock ticks since boot) in files.
func claudeRunsHook(t *testing.T, env *batontest.Env, throughShell bool) (pidFile, startFile string) {
	t.Helper()
	pidFile, startFile = env.Path("claude.pid"), env.Path("claude.start")
	env.WriteFile("payload.json", payload(t, "SessionStart", "abc123", env.Home))
	hook := `"` + batonBin + `" hook claude --profile claude-personal --runtime host < "` + env.Path("payload.json") + `"`
	if throughShell {
		hook = `sh -c '` + strings.ReplaceAll(hook, `'`, `'\\''`) + `; true'`
	}
	env.Stub("claude", batontest.WithScript(`
echo $$ > "`+pidFile+`"
awk '{ print $22 }' /proc/$$/stat > "`+startFile+`"
`+hook))
	if res := env.Command("claude"); res.ExitCode != 0 {
		t.Fatalf("claude stub: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	return pidFile, startFile
}

func TestHook_AgentPIDIsTheAgentsProcessWithItsStartTime(t *testing.T) {
	for name, throughShell := range map[string]bool{"started directly": false, "started through sh -c": true} {
		t.Run(name, func(t *testing.T) {
			env := newEnv(t)
			pidFile, startFile := claudeRunsHook(t, env, throughShell)

			line := eventLines(t, env, ".baton/sessions/claude-abc123.jsonl")[0]

			wantPID, _ := strconv.Atoi(strings.TrimSpace(env.ReadFile(pidFile)))
			wantStart, _ := strconv.ParseUint(strings.TrimSpace(env.ReadFile(startFile)), 10, 64)
			if wantPID == 0 || wantStart == 0 {
				t.Fatalf("the stub left pid %d and start %d", wantPID, wantStart)
			}
			if line["agent_pid"] != float64(wantPID) || line["agent_start"] != float64(wantStart) {
				t.Errorf("agent_pid = %v, agent_start = %v, want the claude process: %d, %d", line["agent_pid"], line["agent_start"], wantPID, wantStart)
			}
			if _, fallback := line["agent_pid_fallback"]; fallback {
				t.Errorf("agent_pid_fallback = %v, want it absent when the Agent was found", line["agent_pid_fallback"])
			}
		})
	}
}

func TestHook_WithoutAnAgentAmongItsNearestAncestorsAgentPIDIsTheDirectParentAndSaysSo(t *testing.T) {
	env := newEnv(t)
	// Ten nested shells separate the hook from whatever is above, which may
	// even be a real Claude when the tests run inside one. The walk up is
	// bounded, so it finds no Agent and falls back to the direct parent, the
	// innermost shell.
	pidFile := env.Path("innermost.pid")
	env.WriteFile("payload.json", payload(t, "SessionStart", "abc123", env.Home))
	env.WriteFile("nest.sh", `#!/bin/sh
n=$1
if [ "$n" -gt 0 ]; then
  sh "$0" $((n - 1))
  true
else
  echo $$ > "`+pidFile+`"
  "`+batonBin+`" hook claude --profile claude-personal --runtime host < "`+env.Path("payload.json")+`"
  true
fi
`, true)
	if res := env.Command(env.Path("nest.sh"), "10"); res.ExitCode != 0 {
		t.Fatalf("nest.sh: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	line := eventLines(t, env, ".baton/sessions/claude-abc123.jsonl")[0]

	wantPID, _ := strconv.Atoi(strings.TrimSpace(env.ReadFile(pidFile)))
	if line["agent_pid"] != float64(wantPID) || wantPID == 0 {
		t.Errorf("agent_pid = %v, want the innermost shell %d", line["agent_pid"], wantPID)
	}
	if line["agent_pid_fallback"] != true {
		t.Errorf("agent_pid_fallback = %v, want true", line["agent_pid_fallback"])
	}
	if start, _ := line["agent_start"].(float64); start <= 0 {
		t.Errorf("agent_start = %v, want the parent's start time", line["agent_start"])
	}
}

func TestHook_ParallelHooksWithPayloadsLargerThanPipeBufNeverInterleave(t *testing.T) {
	env := newEnv(t)
	const hooks = 24
	// Each payload is far above PIPE_BUF (4096) and is made of one letter, so
	// a torn line shows up as a mix of letters or as invalid JSON.
	var wg sync.WaitGroup
	for i := 0; i < hooks; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			filler := strings.Repeat(string(rune('a'+i)), 300_000)
			in := `{"session_id": "abc123", "hook_event_name": "PostToolUse", "cwd": "/x", "tool_response": {"stdout": "` + filler + `"}}`
			if res := env.RunWithStdin(in, hookArgs("claude-personal")...); res.ExitCode != 0 {
				t.Errorf("hook %d: exit code = %d, stderr=%q", i, res.ExitCode, res.Stderr)
			}
		}()
	}
	wg.Wait()

	seen := make(map[string]bool)
	for _, line := range eventLines(t, env, ".baton/sessions/claude-abc123.jsonl") {
		stdout := line["payload"].(map[string]any)["tool_response"].(map[string]any)["stdout"].(string)
		if len(stdout) != 300_000 || strings.Trim(stdout, stdout[:1]) != "" {
			t.Fatalf("a line is torn: %d bytes starting with %q", len(stdout), stdout[:1])
		}
		seen[stdout[:1]] = true
	}
	if len(seen) != hooks {
		t.Errorf("%d different lines, want %d", len(seen), hooks)
	}
}
