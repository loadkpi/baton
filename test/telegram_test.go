package test

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/loadkpi/baton/internal/batontest"
)

// telegramToken is a made-up bot token. The tests only ever send it to the
// local fake server.
const telegramToken = "7000001:AAfake-token-for-tests"

// telegramRequest is one request the fake Telegram server received.
type telegramRequest struct {
	Method string
	Path   string
	Body   string
}

// fakeTelegram is a local HTTP server that stands in for the Bot API. Baton
// reaches it because the config sets its URL.
type fakeTelegram struct {
	t      *testing.T
	server *httptest.Server

	mu       sync.Mutex
	requests []telegramRequest
	status   int    // the status every answer has
	reply    string // the body every answer has
	redirect string // when set, every request is redirected to this URL
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	f := &fakeTelegram{t: t, status: http.StatusOK, reply: `{"ok":true,"result":{}}`}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, telegramRequest{Method: r.Method, Path: r.URL.Path, Body: string(body)})
		status, reply, redirect := f.status, f.reply, f.redirect
		f.mu.Unlock()
		if redirect != "" {
			http.Redirect(w, r, redirect+r.URL.Path, http.StatusTemporaryRedirect)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(f.server.Close)
	return f
}

// answer makes the server answer every request with the given status and body.
func (f *fakeTelegram) answer(status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.reply = status, body
}

// redirectTo makes the server answer every request with a redirect to url.
func (f *fakeTelegram) redirectTo(url string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.redirect = url
}

func (f *fakeTelegram) received() []telegramRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]telegramRequest(nil), f.requests...)
}

// waitFor waits until the server has received n requests and returns them.
func (f *fakeTelegram) waitFor(n int) []telegramRequest {
	f.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if got := f.received(); len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("timed out waiting for %d Telegram requests, have %v", n, f.received())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// telegramConfig is the [telegram] table that points Baton at url.
func telegramConfig(url string) string {
	return fmt.Sprintf("\n[telegram]\napi_url = %q\ntoken_var = \"TELEGRAM_TOKEN\"\nchat_id = 4242\n", url)
}

// telegramEnv makes a Project my-app with the current Task fix-login, a config
// with [telegram] pointing at a fresh fake server, and the token in the
// secrets file.
func telegramEnv(t *testing.T) (*batontest.Env, *fakeTelegram, string, proc) {
	t.Helper()
	env := newEnv(t)
	repo, p := watchRepo(t, env)
	fake := newFakeTelegram(t)
	env.WriteConfig(newConfig(env, telegramConfig(fake.server.URL)))
	writeSecrets(t, env, 0o600, "TELEGRAM_TOKEN="+telegramToken+"\n")
	return env, fake, repo, p
}

// sendMessage is the body Baton posts: the chat and the text.
type sendMessage struct {
	ChatID int64  `json:"chat_id"`
	Text   string `json:"text"`
}

func decodeSendMessage(t *testing.T, r telegramRequest) sendMessage {
	t.Helper()
	var m sendMessage
	if err := json.Unmarshal([]byte(r.Body), &m); err != nil {
		t.Fatalf("body %q is not a sendMessage: %v", r.Body, err)
	}
	return m
}

func TestTelegram_AfterAwayAnApprovalRequestGoesToTelegramAndNotToTheDesktop(t *testing.T) {
	env, fake, repo, p := telegramEnv(t)
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
	if res := env.Run("away"); res.ExitCode != 0 {
		t.Fatalf("away: exit %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	startWatch(t, env)

	hostEvent(t, env, "s1", "2031-03-04T10:00:05Z", "PermissionRequest", repo, p)

	got := fake.waitFor(1)
	if len(got) != 1 || got[0].Method != "POST" || got[0].Path != "/bot"+telegramToken+"/sendMessage" {
		t.Fatalf("requests = %+v, want one POST /bot<token>/sendMessage", got)
	}
	want := sendMessage{ChatID: 4242, Text: "Baton: waiting for approval\nmy-app/fix-login (claude)"}
	if m := decodeSendMessage(t, got[0]); !reflect.DeepEqual(m, want) {
		t.Errorf("message = %+v, want %+v", m, want)
	}
	if calls := env.Calls("notify-send"); len(calls) != 0 {
		t.Errorf("notify-send calls = %v, want none while away", calls)
	}
}

// awayAndWatch goes away and starts the watcher, as a user does.
func awayAndWatch(t *testing.T, env *batontest.Env) {
	t.Helper()
	if res := env.Run("away"); res.ExitCode != 0 {
		t.Fatalf("away: exit %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	startWatch(t, env)
}

// desktopBodies waits for n notify-send calls and returns their bodies.
func desktopBodies(t *testing.T, env *batontest.Env, n int) []string {
	t.Helper()
	var bodies []string
	for _, args := range notifyArgs(notifications(t, env, n)) {
		bodies = append(bodies, args[len(args)-1])
	}
	return bodies
}

func TestTelegram_AfterBackNotificationsGoToTheDesktopAgain(t *testing.T) {
	env, fake, repo, p := telegramEnv(t)
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
	awayAndWatch(t, env)
	if res := env.Run("back"); res.ExitCode != 0 {
		t.Fatalf("back: exit %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	hostEvent(t, env, "s1", "2031-03-04T10:00:05Z", "PermissionRequest", repo, p)

	want := [][]string{{"-a", "baton", "Baton: waiting for approval", "my-app/fix-login (claude)"}}
	if got := notifyArgs(notifications(t, env, 1)); !reflect.DeepEqual(got, want) {
		t.Errorf("notify-send calls = %q, want %q", got, want)
	}
	if got := fake.received(); len(got) != 0 {
		t.Errorf("Telegram requests = %+v, want none after back", got)
	}
}

func TestTelegram_ARunningWatcherFollowsAwayAndBackWithoutARestart(t *testing.T) {
	env, fake, repo, p := telegramEnv(t)
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
	startWatch(t, env) // at the desk

	hostEvent(t, env, "s1", "2031-03-04T10:00:05Z", "PermissionRequest", repo, p)
	notifications(t, env, 1) // desktop

	env.Run("away")
	hostEvent(t, env, "s1", "2031-03-04T10:00:15Z", "PermissionRequest", repo, p)
	fake.waitFor(1) // Telegram

	env.Run("back")
	hostEvent(t, env, "s1", "2031-03-04T10:00:25Z", "PermissionRequest", repo, p)
	notifications(t, env, 2) // desktop again

	if got := len(fake.received()); got != 1 {
		t.Errorf("Telegram requests = %d, want 1", got)
	}
}

func TestTelegram_OnlyStatusTaskAndAgentLeaveTheMachine(t *testing.T) {
	env, fake, repo, p := telegramEnv(t)
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
	awayAndWatch(t, env)

	writeProcEventPayload(t, env, ".baton/sessions/claude-s1.jsonl", "host", "claude-personal", "s1", "2031-03-04T10:00:05Z",
		"PermissionRequest", repo, p, map[string]any{
			"tool_name":  "Bash",
			"tool_input": map[string]any{"command": "curl -H 'Authorization: sk-live-12345' https://internal.example"},
		})
	got := fake.waitFor(1)

	if len(got) != 1 {
		t.Fatalf("requests = %+v, want one", got)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(got[0].Body), &doc); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"chat_id": float64(4242), "text": "Baton: waiting for approval\nmy-app/fix-login (claude)"}
	if !reflect.DeepEqual(doc, want) {
		t.Errorf("body = %v, want exactly %v", doc, want)
	}
}

func TestTelegram_ASessionLostAndAWaitingInputGoToTelegramToo(t *testing.T) {
	env, fake, repo, p := telegramEnv(t)
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
	awayAndWatch(t, env)

	turn(t, env, "s1", repo, p, time.Date(2031, 3, 4, 10, 1, 0, 0, time.UTC), 31*time.Second)

	got := fake.waitFor(1)
	if m := decodeSendMessage(t, got[0]); m.Text != "Baton: waiting for input\nmy-app/fix-login (claude)" {
		t.Errorf("text = %q", m.Text)
	}
}

func TestTelegram_NothingIsEverReadFromTelegram(t *testing.T) {
	env, fake, repo, p := telegramEnv(t)
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
	awayAndWatch(t, env)
	fake.answer(200, `{"ok":true,"result":[{"update_id":1,"message":{"text":"/approve"}}]}`)

	hostEvent(t, env, "s1", "2031-03-04T10:00:05Z", "PermissionRequest", repo, p)
	fake.waitFor(1)
	hostEvent(t, env, "s1", "2031-03-04T10:00:10Z", "PostToolUse", repo, p)
	hostEvent(t, env, "s1", "2031-03-04T10:00:15Z", "PermissionRequest", repo, p)
	got := fake.waitFor(2)

	for _, r := range got {
		if r.Method != "POST" || r.Path != "/bot"+telegramToken+"/sendMessage" {
			t.Errorf("request %+v: Baton sends and does nothing else", r)
		}
	}
}

func TestTelegram_WhenTelegramFailsTheDesktopGetsTheMessageAndOneWarningIsPrintedPerKind(t *testing.T) {
	env, fake, repo, p := telegramEnv(t)
	fake.answer(500, `{"ok":false,"error_code":500,"description":"Internal Server Error"}`)
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
	env.Run("away")
	w := env.Start("watch", "--interval", watchInterval)
	w.WaitStdout("watching")

	hostEvent(t, env, "s1", "2031-03-04T10:00:05Z", "PermissionRequest", repo, p)
	desktopBodies(t, env, 1)
	hostEvent(t, env, "s1", "2031-03-04T10:00:15Z", "PermissionRequest", repo, p)
	bodies := desktopBodies(t, env, 2)

	if want := []string{"my-app/fix-login (claude)", "my-app/fix-login (claude)"}; !reflect.DeepEqual(bodies, want) {
		t.Errorf("desktop bodies = %q, want %q", bodies, want)
	}
	if got := len(fake.received()); got != 2 {
		t.Errorf("Telegram requests = %d, want 2 (it is tried for every message)", got)
	}
	// A different kind of failure is a new warning.
	fake.answer(401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
	hostEvent(t, env, "s1", "2031-03-04T10:00:25Z", "PermissionRequest", repo, p)
	desktopBodies(t, env, 3)
	res := w.Stop()

	warnings := warningLines(res.Stderr)
	if len(warnings) != 2 {
		t.Fatalf("warnings = %q, want one for HTTP 500 and one for HTTP 401", warnings)
	}
	if !strings.Contains(warnings[0], "HTTP 500") || !strings.Contains(warnings[1], "HTTP 401") {
		t.Errorf("warnings = %q", warnings)
	}
	if strings.Contains(res.Stderr, telegramToken) || strings.Contains(res.Stdout, telegramToken) {
		t.Errorf("the token is in the output: stdout=%q stderr=%q", res.Stdout, res.Stderr)
	}
}

// warningLines are the warnings in what a command wrote to standard error.
func warningLines(stderr string) []string {
	var out []string
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "baton: warning:") {
			out = append(out, line)
		}
	}
	return out
}

func TestTelegram_AConnectionErrorNeverShowsTheTokenAndFallsBackToTheDesktop(t *testing.T) {
	env, fake, repo, p := telegramEnv(t)
	fake.server.Close() // nothing listens on the configured URL any more
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
	env.Run("away")
	w := env.Start("watch", "--interval", watchInterval)
	w.WaitStdout("watching")

	hostEvent(t, env, "s1", "2031-03-04T10:00:05Z", "PermissionRequest", repo, p)
	desktopBodies(t, env, 1)
	hostEvent(t, env, "s1", "2031-03-04T10:00:15Z", "PermissionRequest", repo, p)
	desktopBodies(t, env, 2)
	res := w.Stop()

	if warnings := warningLines(res.Stderr); len(warnings) != 1 || !strings.Contains(warnings[0], "telegram") {
		t.Errorf("warnings = %q, want one that names telegram", warnings)
	}
	for name, out := range map[string]string{"stdout": res.Stdout, "stderr": res.Stderr} {
		if strings.Contains(out, telegramToken) || strings.Contains(out, "AAfake") {
			t.Errorf("the token is in %s: %q", name, out)
		}
	}
}

func TestTelegram_ARedirectIsNotFollowedBecauseTheTokenWouldGoWithIt(t *testing.T) {
	env, fake, repo, p := telegramEnv(t)
	elsewhere := newFakeTelegram(t)
	fake.redirectTo(elsewhere.server.URL)
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
	awayAndWatch(t, env)

	hostEvent(t, env, "s1", "2031-03-04T10:00:05Z", "PermissionRequest", repo, p)
	desktopBodies(t, env, 1) // Telegram failed, so the desktop got it

	if got := elsewhere.received(); len(got) != 0 {
		t.Errorf("the redirect target received %+v, want nothing", got)
	}
}

func TestAway_RefusesWithoutAWorkingTelegramSetupAndStaysAtTheDesk(t *testing.T) {
	cases := []struct {
		name   string
		config string
		secret string
		want   string
	}{
		{"no table", "", "TELEGRAM_TOKEN=" + telegramToken + "\n", "telegram is not configured"},
		{"token missing", telegramConfig("https://api.telegram.org"), "OTHER=x\n", "TELEGRAM_TOKEN is not set"},
		{"token pasted into the config", "\n[telegram]\ntoken_var = \"" + telegramToken + "\"\nchat_id = 1\n", "", "token_var"},
		{"no chat id", "\n[telegram]\ntoken_var = \"TELEGRAM_TOKEN\"\n", "TELEGRAM_TOKEN=" + telegramToken + "\n", "chat_id"},
		{"plain http to a remote host", telegramConfig("http://telegram.example.net"), "TELEGRAM_TOKEN=" + telegramToken + "\n", "https"},
		{"misspelled key", "\n[telegram]\ntoken_var = \"TELEGRAM_TOKEN\"\nchat_id = 1\nchatid = 2\n", "TELEGRAM_TOKEN=" + telegramToken + "\n", "chatid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newEnv(t)
			env.WriteConfig(newConfig(env, tc.config))
			writeSecrets(t, env, 0o600, tc.secret)

			res := env.Run("away")

			if res.ExitCode != 1 || !strings.Contains(res.Stderr, tc.want) {
				t.Errorf("exit %d, stderr=%q, want exit 1 mentioning %q", res.ExitCode, res.Stderr, tc.want)
			}
			if strings.Contains(res.Stderr, telegramToken) || strings.Contains(res.Stdout, telegramToken) {
				t.Errorf("the token is in the output: %q %q", res.Stdout, res.Stderr)
			}
			if env.Exists(".baton/away") {
				t.Error("away state was set although away refused")
			}
		})
	}
}

func TestAway_AcceptsTheDefaultApiUrlAChatNameAndLocalPlainHttp(t *testing.T) {
	for name, table := range map[string]string{
		"defaults":  "\n[telegram]\ntoken_var = \"TELEGRAM_TOKEN\"\nchat_id = -100123\n",
		"chat name": "\n[telegram]\ntoken_var = \"TELEGRAM_TOKEN\"\nchat_id = \"@my_channel\"\n",
		"localhost": "\n[telegram]\napi_url = \"http://localhost:8081/\"\ntoken_var = \"TELEGRAM_TOKEN\"\nchat_id = 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			env := newEnv(t)
			env.WriteConfig(newConfig(env, table))
			writeSecrets(t, env, 0o600, "TELEGRAM_TOKEN="+telegramToken+"\n")

			res := env.Run("away")

			if res.ExitCode != 0 {
				t.Errorf("exit %d, stderr=%q", res.ExitCode, res.Stderr)
			}
		})
	}
}

func TestBack_IsHarmlessWhenNotAwayAndAwayTwiceIsFine(t *testing.T) {
	env, _, _, _ := telegramEnv(t)

	for _, args := range [][]string{{"back"}, {"away"}, {"away"}, {"back"}, {"back"}} {
		if res := env.Run(args...); res.ExitCode != 0 {
			t.Errorf("baton %v: exit %d, stderr=%q", args, res.ExitCode, res.Stderr)
		}
	}
	if env.Exists(".baton/away") {
		t.Error("away state still set after back")
	}
}

func TestTelegram_ASecretsFileOthersCanReadIsRefusedWithoutShowingAnything(t *testing.T) {
	env, _, _, _ := telegramEnv(t)
	writeSecrets(t, env, 0o644, "TELEGRAM_TOKEN="+telegramToken+"\n")

	res := env.Run("away")

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "chmod 600") || strings.Contains(res.Stderr, telegramToken) {
		t.Errorf("exit %d, stderr=%q", res.ExitCode, res.Stderr)
	}
}

func TestTelegram_ADescriptionThatEchoesTheTokenIsRedacted(t *testing.T) {
	env, fake, repo, p := telegramEnv(t)
	fake.answer(400, `{"ok":false,"error_code":400,"description":"bad request for bot `+telegramToken+`"}`)
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
	env.Run("away")
	w := env.Start("watch", "--interval", watchInterval)
	w.WaitStdout("watching")

	hostEvent(t, env, "s1", "2031-03-04T10:00:05Z", "PermissionRequest", repo, p)
	desktopBodies(t, env, 1)
	res := w.Stop()

	if strings.Contains(res.Stderr, telegramToken) || !strings.Contains(res.Stderr, "HTTP 400") {
		t.Errorf("stderr = %q, want the status without the token", res.Stderr)
	}
}

// filesContaining lists the files under root whose content has needle in it.
func filesContaining(t *testing.T, root, needle string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), needle) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestTelegram_TheTokenNeverEntersASandbox(t *testing.T) {
	env := newEnv(t)
	repo, home := launchProject(t, env)
	env.SourceFile("skills/greet/SKILL.md", "---\nname: greet\ndescription: Say hello\n---\nHello.\n")
	env.SourceFile("AGENTS.md", "Be kind.\n")
	env.CommitSource("add content")
	env.WriteConfig(standardConfig(env) + "\n[projects.app]\ntags = []\n" + telegramConfig("https://api.telegram.org"))
	writeSecrets(t, env, 0o600, "TELEGRAM_TOKEN="+telegramToken+"\n")

	for _, args := range [][]string{{"sync"}, {"away"}, {"new", "app", "fix-login", "--agent", "claude"}} {
		if res := env.Run(args...); res.ExitCode != 0 {
			t.Fatalf("baton %v: exit %d, stderr=%q", args, res.ExitCode, res.Stderr)
		}
	}
	run := runTabLine(t, env)

	if found := filesContaining(t, home, telegramToken); len(found) != 0 {
		t.Errorf("the token is in the sandbox home: %v", found)
	}
	if found := filesContaining(t, filepath.Join(repo, ".ai-sandbox"), telegramToken); len(found) != 0 {
		t.Errorf("the token is in the sandbox directory: %v", found)
	}
	if found := filesContaining(t, env.Path(".baton/projects"), telegramToken); len(found) != 0 {
		t.Errorf("the token is in the Project's mounted directory: %v", found)
	}
	if strings.Contains(strings.Join(run, " "), telegramToken) {
		t.Errorf("the token is in the docker run arguments: %q", run)
	}
	if !env.Exists(filepath.Join(home, ".claude", "skills", "greet", "SKILL.md")) {
		t.Error("the sandbox Profile was not synced, so the check above proved nothing")
	}
}

func TestTelegram_ATokenThatWentMissingWhileAwayFallsBackToTheDesktopWithOneWarning(t *testing.T) {
	env, _, repo, p := telegramEnv(t)
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
	env.Run("away")
	w := env.Start("watch", "--interval", watchInterval)
	w.WaitStdout("watching")
	writeSecrets(t, env, 0o600, "OTHER=1\n")

	hostEvent(t, env, "s1", "2031-03-04T10:00:05Z", "PermissionRequest", repo, p)
	desktopBodies(t, env, 1)
	res := w.Stop()

	if warnings := warningLines(res.Stderr); len(warnings) != 1 || !strings.Contains(warnings[0], "TELEGRAM_TOKEN is not set") {
		t.Errorf("warnings = %q, want one that names the missing variable", warnings)
	}
}
