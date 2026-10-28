package test

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/loadkpi/baton/internal/batontest"
)

var gap = regexp.MustCompile(` {2,}`)

// longRows runs `baton ls --long` and returns the rows under the header as
// cells (the tab writer pads with two or more spaces; a cell keeps its own
// single spaces), and the header.
func longRows(t *testing.T, env *batontest.Env) (header []string, rows [][]string) {
	t.Helper()
	res := env.Run("ls", "--long")
	if res.ExitCode != 0 {
		t.Fatalf("ls --long: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	header = gap.Split(lines[0], -1)
	for _, l := range lines[1:] {
		if l == "" {
			break
		}
		rows = append(rows, gap.Split(l, -1))
	}
	return header, rows
}

// longRow is the only row of `baton ls --long`, keyed by the header.
func longRow(t *testing.T, env *batontest.Env) map[string]string {
	t.Helper()
	header, rows := longRows(t, env)
	if len(rows) != 1 || len(rows[0]) != len(header) {
		t.Fatalf("header = %q, rows = %q, want one row with a cell for each column", header, rows)
	}
	out := map[string]string{}
	for i, h := range header {
		out[h] = rows[0][i]
	}
	return out
}

// before is the time d ago, as an Event records it.
func before(d time.Duration) string {
	return time.Now().UTC().Add(-d).Format(time.RFC3339Nano)
}

// unjudged is a process Baton cannot judge, so that a Session stays as its
// Events say.
func unjudged(t *testing.T) proc {
	t.Helper()
	return proc{host: thisHost(t), pid: deadPID(t), start: 4242, fallback: true}
}

// claudeEvent appends an Event of a host Claude Session with the payload.
func claudeEvent(t *testing.T, env *batontest.Env, sessionID, ts, event, cwd string, payload map[string]any) {
	t.Helper()
	writeProcEventPayload(t, env, ".baton/sessions/claude-"+sessionID+".jsonl", "host", "claude-personal", sessionID, ts, event, cwd, unjudged(t), payload)
}

// titleLine is a line of a Claude Code transcript that names the Session.
func titleLine(kind, title string) string {
	field := map[string]string{"ai-title": "aiTitle", "custom-title": "customTitle"}[kind]
	return fmt.Sprintf(`{"type":%q,%q:%q,"sessionId":"s1"}`, kind, field, title) + "\n"
}

// filler is n bytes of transcript lines that name nothing.
func filler(n int) string {
	line := `{"type":"assistant","message":{"content":"` + strings.Repeat("x", 200) + `"}}` + "\n"
	return strings.Repeat(line, n/len(line)+1)
}

func TestNames_AClaudeSessionIsNamedByTheNewestTitleOfItsTranscriptWhichIsDeepInAFileLargerThanAReadChunk(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "app")
	transcript := env.Path(".claude-personal", "projects", "p", "s1.jsonl")
	env.WriteFile(transcript, titleLine("ai-title", "Old name")+filler(100<<10)+titleLine("ai-title", "Fix the parser")+filler(150<<10))
	claudeEvent(t, env, "s1", before(time.Minute), "Stop", repo, map[string]any{"transcript_path": transcript})

	row := longRow(t, env)

	if row["NAME"] != "Fix the parser" {
		t.Errorf("NAME = %q, want %q", row["NAME"], "Fix the parser")
	}
}

func TestNames_ANameTheUserSetCountsWhenItIsTheNewestTitle(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "app")
	transcript := env.Path(".claude-personal", "projects", "p", "s1.jsonl")
	env.WriteFile(transcript, titleLine("ai-title", "Generated")+filler(10<<10)+titleLine("custom-title", "Chosen by me")+filler(10<<10))
	claudeEvent(t, env, "s1", before(time.Minute), "Stop", repo, map[string]any{"transcript_path": transcript})

	row := longRow(t, env)

	if row["NAME"] != "Chosen by me" {
		t.Errorf("NAME = %q, want %q", row["NAME"], "Chosen by me")
	}
}

func TestNames_ATitleBeyondTheReadLimitIsNotLookedFor(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "app")
	transcript := env.Path(".claude-personal", "projects", "p", "s1.jsonl")
	env.WriteFile(transcript, titleLine("ai-title", "Too far back")+filler(5<<20))
	claudeEvent(t, env, "s1", before(time.Hour), "UserPromptSubmit", repo, map[string]any{"transcript_path": transcript, "prompt": "the first words"})

	row := longRow(t, env)

	if row["NAME"] != "the first words" {
		t.Errorf("NAME = %q, want the fallback, the first prompt", row["NAME"])
	}
}

func TestNames_WithoutATitleTheNameIsTheFirstPromptOnOneLine(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "app")
	claudeEvent(t, env, "s1", before(3*time.Hour), "SessionStart", repo, nil)
	claudeEvent(t, env, "s1", before(2*time.Hour), "UserPromptSubmit", repo, map[string]any{"prompt": "Fix the\nflaky   test\n\nplease"})
	claudeEvent(t, env, "s1", before(time.Hour), "UserPromptSubmit", repo, map[string]any{"prompt": "and the second prompt"})

	row := longRow(t, env)

	if row["NAME"] != "Fix the flaky test please" {
		t.Errorf("NAME = %q, want the first prompt on one line", row["NAME"])
	}
}

func TestNames_ASessionWithNeitherATitleNorAPromptHasADash(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "app")
	claudeEvent(t, env, "s1", before(time.Hour), "SessionStart", repo, map[string]any{"transcript_path": env.Path("missing.jsonl")})

	row := longRow(t, env)

	if row["NAME"] != "-" {
		t.Errorf("NAME = %q, want -", row["NAME"])
	}
}

// codexIndexLine is a line of Codex's session_index.jsonl.
func codexIndexLine(id, name string) string {
	return fmt.Sprintf(`{"id":%q,"thread_name":%q,"updated_at":"2031-03-04T10:00:00Z"}`, id, name) + "\n"
}

// sandboxMounts records the mounts the sandbox of Project app was started with:
// the Project at /workspace and its home at /home/sandbox.
func sandboxMounts(t *testing.T, env *batontest.Env, repo string) {
	t.Helper()
	env.WriteFile(".baton/mounts/app.json", fmt.Sprintf(`{"v":1,"mounts":[{"host":%q,"container":"/workspace"},{"host":%q,"container":"/home/sandbox"}]}`,
		repo, filepath.Join(repo, ".ai-sandbox", "home")))
}

func TestNames_ASandboxCodexSessionIsNamedFromTheIndexUnderTheProjectsSandboxHome(t *testing.T) {
	env := newEnv(t)
	repo, home := sandboxProject(t, env, "app")
	sandboxMounts(t, env, repo)
	env.WriteFile(filepath.Join(home, ".codex", "session_index.jsonl"), codexIndexLine("thr_1", "First name")+codexIndexLine("thr_2", "Another thread")+codexIndexLine("thr_1", "Sandboxed work"))
	writeAgentProcEvent(t, env, "codex", ".baton/projects/app/sessions/codex-thr_1.jsonl", "sandbox", "app.codex", "thr_1", before(time.Minute), "Stop", "/workspace", unjudged(t), map[string]any{})

	row := longRow(t, env)

	if row["NAME"] != "Sandboxed work" {
		t.Errorf("NAME = %q, want %q", row["NAME"], "Sandboxed work")
	}
}

func TestNames_ASandboxClaudeSessionIsNamedFromItsTranscriptUnderTheProjectsSandboxHome(t *testing.T) {
	env := newEnv(t)
	repo, home := sandboxProject(t, env, "app")
	sandboxMounts(t, env, repo)
	env.WriteFile(filepath.Join(home, ".claude", "projects", "p", "s1.jsonl"), titleLine("ai-title", "Boxed Claude"))
	writeProcEventPayload(t, env, ".baton/projects/app/sessions/claude-s1.jsonl", "sandbox", "app.claude", "s1", before(time.Minute), "Stop", "/workspace", unjudged(t),
		map[string]any{"transcript_path": "/home/sandbox/.claude/projects/p/s1.jsonl"})

	row := longRow(t, env)

	if row["NAME"] != "Boxed Claude" {
		t.Errorf("NAME = %q, want %q", row["NAME"], "Boxed Claude")
	}
}

func TestNames_AComposeSessionsFilesAreNotReadItFallsBackToTheFirstPrompt(t *testing.T) {
	env := newEnv(t)
	transcript := env.Path("somewhere", "s1.jsonl") // a host path with the same name as the one inside the container
	env.WriteFile(transcript, titleLine("ai-title", "Must not be read"))
	payload := map[string]any{"transcript_path": transcript, "prompt": "ask the container"}
	writeProcEventPayload(t, env, ".baton/projects/app/sessions/claude-s1.jsonl", "compose", "app.claude", "s1", before(time.Hour), "UserPromptSubmit", "/workspace", unjudged(t), payload)

	row := longRow(t, env)

	if row["NAME"] != "ask the container" {
		t.Errorf("NAME = %q, want the first prompt", row["NAME"])
	}
}

func TestLastAction_IsTheNewestEventInWordsWithItsAge(t *testing.T) {
	tests := []struct {
		name    string
		event   string
		payload map[string]any
		age     time.Duration
		want    string
	}{
		{"command", "PreToolUse", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "go test ./...", "description": "Run"}}, 12 * time.Second, "12s Bash: go test ./..."},
		{"file", "PostToolUse", map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"file_path": "/src/a.go", "old_string": "x"}}, 4*time.Minute + 20*time.Second, "4m Edit: /src/a.go"},
		{"pattern", "PostToolUse", map[string]any{"tool_name": "Grep", "tool_input": map[string]any{"pattern": "TODO", "path": "/src"}}, 3 * time.Hour, "3h Grep: TODO"},
		{"url", "PreToolUse", map[string]any{"tool_name": "WebFetch", "tool_input": map[string]any{"url": "https://example.com/a"}}, 2 * 24 * time.Hour, "2d WebFetch: https://example.com/a"},
		{"no detail", "PreToolUse", map[string]any{"tool_name": "TodoWrite", "tool_input": map[string]any{"todos": []any{}}}, 12 * time.Second, "12s TodoWrite"},
		{"skill", "PreToolUse", map[string]any{"tool_name": "Skill", "tool_input": map[string]any{"skill": "baton-handoff"}}, 12 * time.Second, "12s skill baton-handoff"},
		{"failure", "PostToolUseFailure", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "npm test"}}, 12 * time.Second, "12s Bash failed: npm test"},
		{"permission", "PermissionRequest", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "rm -rf build"}}, 12 * time.Second, "12s asks: Bash: rm -rf build"},
		{"prompt", "UserPromptSubmit", map[string]any{"prompt": "what is\nthe trial word?"}, 12 * time.Second, "12s prompt: what is the trial word?"},
		{"stop", "Stop", nil, 12 * time.Second, "12s done"},
		{"interrupt", "Interrupt", nil, 12 * time.Second, "12s interrupted"},
		{"start", "SessionStart", nil, 12 * time.Second, "12s started"},
		{"end", "SessionEnd", nil, 12 * time.Second, "12s ended"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t)
			repo := newRepo(t, env, "app")
			claudeEvent(t, env, "s1", before(tt.age+10*time.Minute), "SessionStart", repo, nil)
			claudeEvent(t, env, "s1", before(tt.age), tt.event, repo, tt.payload)

			row := longRow(t, env)

			got := row["LAST"]
			if tt.age < time.Minute {
				got = regexp.MustCompile(`^\d+s`).ReplaceAllString(got, "12s") // ls runs a moment later
			}
			if got != tt.want {
				t.Errorf("LAST = %q, want %q", row["LAST"], tt.want)
			}
		})
	}
}

func TestLastAction_ComesFromTheMaskedEventSoNoSecretAppears(t *testing.T) {
	env := newEnv(t)
	res := env.RunWithStdin(bashPayload(t, "s1", "curl -H 'Authorization: Bearer "+tokAnthropic+"' https://example.com", nil), hookArgs("claude-personal")...)
	if res.ExitCode != 0 {
		t.Fatalf("hook: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	out := env.Run("ls", "--long").Stdout

	if strings.Contains(out, tokAnthropic) {
		t.Errorf("ls --long shows a secret: %q", out)
	}
	if !strings.Contains(out, mask) {
		t.Errorf("ls --long = %q, want the secret masked as %s", out, mask)
	}
}

func TestLs_LongAddsNameAndLastAfterSessionAndTheDefaultOutputIsUnchanged(t *testing.T) {
	env := newEnv(t)
	repo := newRepo(t, env, "app")
	claudeEvent(t, env, "aaaa1111-0000", before(time.Hour), "UserPromptSubmit", repo, map[string]any{"prompt": "hello there"})

	header, rows := longRows(t, env)
	plainOut := env.Run("ls").Stdout

	wantHeader := []string{"PROJECT", "TASK", "AGENT", "SESSION", "NAME", "LAST", "PROFILE", "RUNTIME", "STATUS"}
	if strings.Join(header, "|") != strings.Join(wantHeader, "|") {
		t.Errorf("header = %q, want %q", header, wantHeader)
	}
	if len(rows) != 1 || rows[0][3] != "aaaa1111" || rows[0][4] != "hello there" || rows[0][5] != "1h prompt: hello there" || rows[0][8] != "running" {
		t.Errorf("rows = %q", rows)
	}
	if !strings.HasPrefix(plainOut, "PROJECT  TASK ") || strings.Contains(plainOut, "NAME") || strings.Contains(plainOut, "hello there") {
		t.Errorf("ls = %q, want the columns it always had", plainOut)
	}
}

func TestLs_AnArgumentOtherThanLongIsAUsageError(t *testing.T) {
	env := newEnv(t)

	res := env.Run("ls", "--wide")

	if res.ExitCode != 2 || !strings.Contains(res.Stderr, "usage: baton ls [--long]") {
		t.Errorf("exit code = %d, stderr=%q, want 2 and the usage", res.ExitCode, res.Stderr)
	}
}

// topNamed makes one Session that waits for input, with a title and a last
// action, and returns the transcript.
func topNamed(t *testing.T, env *batontest.Env) string {
	t.Helper()
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "app")
	transcript := env.Path(".claude-personal", "projects", "p", "s1.jsonl")
	env.WriteFile(transcript, titleLine("ai-title", "Fix the parser"))
	claudeEvent(t, env, "s1", before(4*time.Hour), "SessionStart", repo, map[string]any{"transcript_path": transcript})
	claudeEvent(t, env, "s1", before(3*time.Hour), "PreToolUse", repo,
		map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "go test ./..."}})
	return transcript
}

func TestTop_ShowsNameAndLastInTheirOwnColumnsOnAWideTerminal(t *testing.T) {
	env := newEnv(t)
	topNamed(t, env)

	term := env.StartTerminalSized(140, "top", "--interval", "50ms")
	term.Expect("SESSION  NAME")
	term.Expect("LAST")
	term.Expect("PROFILE")
	term.Expect("Fix the parser  ")
	term.Expect("3h Bash: go test ./...  ")
	quitTop(t, term)
}

func TestTop_OnANarrowTerminalLastGoesOnADimmedLineUnderTheSelectedRowOnly(t *testing.T) {
	env := newEnv(t)
	topNamed(t, env)
	repo2 := newRepo(t, env, "other")
	claudeEvent(t, env, "s2", before(2*time.Hour), "Stop", repo2, nil)

	term := env.StartTerminalSized(90, "top", "--interval", "50ms")
	term.Expect("SESSION  NAME")
	term.Expect("PROFILE") // there is no LAST between them
	term.Expect("\x1b[2m    2h done\x1b[0m")
	term.Send("j")
	term.Expect("\x1b[2m    3h Bash: go test ./...\x1b[0m")
	quitTop(t, term)
}

func TestTop_ANameThatChangesInTheTranscriptIsShownAtTheNextLook(t *testing.T) {
	env := newEnv(t)
	transcript := topNamed(t, env)
	term := env.StartTerminalSized(140, "top", "--interval", "50ms")
	term.Expect("Fix the parser")

	env.WriteFile(transcript, env.ReadFile(transcript)+titleLine("ai-title", "Add the cache"))

	term.Expect("Add the cache")
	quitTop(t, term)
}

func TestTop_ALongNameIsCutWithAnEllipsisToTheRoomThatIsLeft(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, ""))
	repo := newRepo(t, env, "app")
	claudeEvent(t, env, "s1", before(time.Hour), "UserPromptSubmit", repo, map[string]any{"prompt": strings.Repeat("word ", 40)})

	term := env.StartTerminalSized(100, "top", "--interval", "50ms")
	term.Expect("  word wor…  1h prompt…  claude-personal  host     running")
	quitTop(t, term)
}
