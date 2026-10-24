package test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/loadkpi/baton/internal/batontest"
)

// noSuchSound is a theme name that no desktop has, so that the real themes
// under /usr/share/sounds can never answer for it.
const noSuchSound = "baton-test-no-such-sound"

// soundConfig is the [watch] table that sets sound to the TOML value.
func soundConfig(tomlValue string) string { return "\n[watch]\nsound = " + tomlValue + "\n" }

// themeFile is where the fake sound theme of the Env keeps a file.
func themeFile(env *batontest.Env, name string) string {
	return env.Path(".local", "share", "sounds", "freedesktop", "stereo", name)
}

// putSounds writes fake sound files into the theme of the Env and returns the
// path of the last one.
func putSounds(env *batontest.Env, names ...string) string {
	for _, name := range names {
		env.WriteFile(themeFile(env, name), "not really a sound")
	}
	return themeFile(env, names[len(names)-1])
}

// askForApproval makes the Session ask for approval, as the Agent does, with a
// watcher already running.
func askForApproval(t *testing.T, env *batontest.Env, repo string, p proc, ts string) {
	t.Helper()
	hostEvent(t, env, "s1", ts, "PermissionRequest", repo, p)
}

// soundEnv is watchRepo with a Session that has started, and the config set.
func soundEnv(t *testing.T, extraConfig string) (*batontest.Env, string, proc) {
	t.Helper()
	env := newEnv(t)
	repo, p := watchRepo(t, env)
	env.WriteConfig(newConfig(env, extraConfig))
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
	return env, repo, p
}

// playerCalls waits until the stub called name has been called n times.
func playerCalls(t *testing.T, env *batontest.Env, name string, n int) [][]string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		calls := env.Calls(name)
		if len(calls) >= n {
			return notifyArgs(calls)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d calls of %s, have %v", n, name, calls)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// oldNotification is what notify-send gets, with or without a sound.
var oldNotification = []string{"-a", "baton", "Baton: waiting for approval", "my-app/fix-login (claude)"}

func TestWatchSound_ANameIsPlayedByPwPlayAfterTheNotification(t *testing.T) {
	env, repo, p := soundEnv(t, soundConfig(`"message-new-instant"`))
	file := putSounds(env, "message-new-instant.oga")
	startWatch(t, env)

	askForApproval(t, env, repo, p, "2031-03-04T10:00:05Z")

	if got, want := playerCalls(t, env, "pw-play", 1), [][]string{{file}}; !reflect.DeepEqual(got, want) {
		t.Errorf("pw-play calls = %q, want %q", got, want)
	}
	if got, want := notifyArgs(notifications(t, env, 1)), [][]string{oldNotification}; !reflect.DeepEqual(got, want) {
		t.Errorf("notify-send calls = %q, want %q (no hint)", got, want)
	}
	if got := env.Calls("paplay"); len(got) != 0 {
		t.Errorf("paplay calls = %v, want none while pw-play is there", got)
	}
}

func TestWatchSound_WithoutASoundNothingIsPlayedAndNotifySendIsAsItWas(t *testing.T) {
	cases := []struct {
		name   string
		config string
	}{
		{"no table", ""},
		{"table without the key", "\n[watch]\n"},
		{"empty sound", soundConfig(`""`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, repo, p := soundEnv(t, tc.config)
			putSounds(env, "message-new-instant.oga")
			startWatch(t, env)

			askForApproval(t, env, repo, p, "2031-03-04T10:00:05Z")

			// The sentinel is told after the approval, so by then the approval
			// has been notified, and no player was started for either.
			if got, want := othersAfterSentinel(t, env), [][]string{oldNotification}; !reflect.DeepEqual(got, want) {
				t.Errorf("notify-send calls = %q, want %q", got, want)
			}
			if pw, pa := env.Calls("pw-play"), env.Calls("paplay"); len(pw)+len(pa) != 0 {
				t.Errorf("player calls = %v %v, want none", pw, pa)
			}
		})
	}
}

func TestWatchSound_PaplayPlaysWhenPwPlayIsNotOnPath(t *testing.T) {
	env, repo, p := soundEnv(t, soundConfig(`"message-new-instant"`))
	file := putSounds(env, "message-new-instant.oga")
	env.Without("pw-play")
	startWatch(t, env)

	askForApproval(t, env, repo, p, "2031-03-04T10:00:05Z")

	if got, want := playerCalls(t, env, "paplay", 1), [][]string{{file}}; !reflect.DeepEqual(got, want) {
		t.Errorf("paplay calls = %q, want %q", got, want)
	}
	if got := env.Calls("pw-play"); len(got) != 0 {
		t.Errorf("pw-play calls = %v, want none", got)
	}
}

func TestWatchSound_WithNeitherPlayerOneWarningAtStartAndTheNotificationIsStillSent(t *testing.T) {
	env, repo, p := soundEnv(t, soundConfig(`"message-new-instant"`))
	putSounds(env, "message-new-instant.oga")
	env.Without("pw-play", "paplay")
	w := startWatch(t, env)

	askForApproval(t, env, repo, p, "2031-03-04T10:00:05Z")
	notifications(t, env, 1)
	askForApproval(t, env, repo, p, "2031-03-04T10:00:15Z")

	if got, want := notifyArgs(notifications(t, env, 2)), [][]string{oldNotification, oldNotification}; !reflect.DeepEqual(got, want) {
		t.Errorf("notify-send calls = %q, want %q", got, want)
	}
	res := w.Stop()
	warnings := warningLines(res.Stderr)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "pw-play") || !strings.Contains(warnings[0], "paplay") {
		t.Errorf("warnings = %q, want one that names both players", warnings)
	}
}

func TestWatchSound_TheFirstExtensionThatExistsWinsInTheOrderOgaOggWav(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  string
	}{
		{"oga before ogg and wav", []string{"x.wav", "x.ogg", "x.oga"}, "x.oga"},
		{"ogg before wav", []string{"x.wav", "x.ogg"}, "x.ogg"},
		{"wav alone", []string{"x.wav"}, "x.wav"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, repo, p := soundEnv(t, soundConfig(`"x"`))
			putSounds(env, tc.files...)
			startWatch(t, env)

			askForApproval(t, env, repo, p, "2031-03-04T10:00:05Z")

			if got, want := playerCalls(t, env, "pw-play", 1), [][]string{{themeFile(env, tc.want)}}; !reflect.DeepEqual(got, want) {
				t.Errorf("pw-play calls = %q, want %q", got, want)
			}
		})
	}
}

func TestWatchSound_AnAbsolutePathAndAHomePathAreFilesPlayedAsTheyAre(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tomlValue func(env *batontest.Env) string
	}{
		{"absolute", func(env *batontest.Env) string { return `"` + env.Path("sounds", "ping.wav") + `"` }},
		{"home", func(*batontest.Env) string { return `"~/sounds/ping.wav"` }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newEnv(t)
			repo, p := watchRepo(t, env)
			env.WriteConfig(newConfig(env, soundConfig(tc.tomlValue(env))))
			env.WriteFile("sounds/ping.wav", "not really a sound")
			hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
			startWatch(t, env)

			askForApproval(t, env, repo, p, "2031-03-04T10:00:05Z")

			if got, want := playerCalls(t, env, "pw-play", 1), [][]string{{env.Path("sounds", "ping.wav")}}; !reflect.DeepEqual(got, want) {
				t.Errorf("pw-play calls = %q, want %q", got, want)
			}
		})
	}
}

func TestWatchSound_AWrongSoundStopsTheWatcherBeforeItStartsAndCallsNothing(t *testing.T) {
	cases := []struct {
		name    string
		sound   string // the TOML value
		prepare func(env *batontest.Env)
	}{
		{"a space", `"message new"`, nil},
		{"a colon", `"string:evil"`, nil},
		{"a letter outside ASCII", `"сигнал"`, nil},
		{"a newline in a name", `"a\nb"`, nil},
		{"65 characters", `"` + strings.Repeat("a", 65) + `"`, nil},
		{"a relative path", `"sounds/ping.wav"`, func(env *batontest.Env) { env.WriteFile("sounds/ping.wav", "x") }},
		{"a path with a newline", `"/tmp/a\nb"`, nil},
		{"a path with NUL", `"/tmp/a\u0000b"`, nil},
		{"~ alone", `"~"`, nil},
		{"another user's home", `"~root/ping.wav"`, nil},
		{"not a string", `5`, nil},
		{"a name found nowhere", `"` + noSuchSound + `"`, nil},
		{"a path that is not there", `"/nonexistent-baton-test/ping.wav"`, nil},
		{"a path that is a directory", `"~/sounds"`, func(env *batontest.Env) { env.MakeProfileDir("sounds") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, _, _ := soundEnv(t, soundConfig(tc.sound))
			if tc.prepare != nil {
				tc.prepare(env)
			}

			res := env.Run("watch", "--interval", watchInterval)

			if res.ExitCode != 1 {
				t.Errorf("exit code = %d, want 1; stderr=%q", res.ExitCode, res.Stderr)
			}
			if !strings.Contains(res.Stderr, "watch.sound") || !strings.Contains(res.Stderr, env.ConfigPath()) {
				t.Errorf("stderr = %q, want it to name watch.sound and %s", res.Stderr, env.ConfigPath())
			}
			if strings.Contains(res.Stderr, noSuchSound) || strings.Contains(res.Stderr, "ping.wav") {
				t.Errorf("stderr = %q quotes the value", res.Stderr)
			}
			if strings.Contains(res.Stdout, "watching") {
				t.Errorf("stdout = %q, the watcher started", res.Stdout)
			}
			for _, name := range []string{"notify-send", "pw-play", "paplay"} {
				if calls := env.Calls(name); len(calls) != 0 {
					t.Errorf("%s calls = %v, want none", name, calls)
				}
			}
		})
	}
}

func TestWatchSound_AMissingNameSaysWhereItLooked(t *testing.T) {
	env, _, _ := soundEnv(t, soundConfig(`"`+noSuchSound+`"`))

	res := env.Run("watch", "--interval", watchInterval)

	for _, dir := range []string{env.Path(".local", "share", "sounds"), "/usr/local/share/sounds", "/usr/share/sounds"} {
		if !strings.Contains(res.Stderr, dir) {
			t.Errorf("stderr = %q, want it to name %s", res.Stderr, dir)
		}
	}
}

func TestWatchSound_ANameInTheUserDirectoryBeatsTheSystemThemes(t *testing.T) {
	// message-new-instant is in the freedesktop theme of many systems; the
	// user's directory is searched first, so the fake one is the one played.
	env, repo, p := soundEnv(t, soundConfig(`"message-new-instant"`))
	file := putSounds(env, "message-new-instant.wav")
	startWatch(t, env)

	askForApproval(t, env, repo, p, "2031-03-04T10:00:05Z")

	if got, want := playerCalls(t, env, "pw-play", 1), [][]string{{file}}; !reflect.DeepEqual(got, want) {
		t.Errorf("pw-play calls = %q, want %q", got, want)
	}
}

func TestWatchSound_AnInvalidSoundDoesNotBreakTheCommandsThatDoNotReadIt(t *testing.T) {
	env := newEnv(t)
	repo, _ := watchRepo(t, env)
	env.WriteConfig(newConfig(env, soundConfig(`"not valid!"`)))

	if res := env.Run("ls"); res.ExitCode != 0 || strings.Contains(res.Stderr, "watch") {
		t.Errorf("ls: exit code = %d, stderr=%q, want 0 and no complaint", res.ExitCode, res.Stderr)
	}
	if res := env.Run("task", "status", "my-app/fix-login", "open"); res.ExitCode != 0 {
		t.Errorf("task status: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if res := env.Run("new", repo, "second", "--agent", "claude"); res.ExitCode != 0 {
		t.Errorf("new: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
}

func TestWatchSound_AMisspelledKeyOfTheTableIsAnError(t *testing.T) {
	env := newEnv(t)
	env.WriteConfig(newConfig(env, "\n[watch]\nsounds = \"x\"\n"))

	res := env.Run("watch", "--interval", watchInterval)

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "sounds") {
		t.Errorf("exit code = %d, stderr=%q, want 1 naming the key", res.ExitCode, res.Stderr)
	}
}

func TestWatchSound_APlayerThatHangsHoldsUpNeitherTheNextNotificationNorTheWatcher(t *testing.T) {
	env, repo, p := soundEnv(t, soundConfig(`"message-new-instant"`))
	putSounds(env, "message-new-instant.oga")
	env.Stub("pw-play", batontest.WithScript("exec sleep 60"))
	startWatch(t, env)

	askForApproval(t, env, repo, p, "2031-03-04T10:00:05Z")
	playerCalls(t, env, "pw-play", 1)
	askForApproval(t, env, repo, p, "2031-03-04T10:00:15Z")

	// Both notifications and both sounds arrive while the first player is
	// still running, and overlap: there is no queue.
	notifications(t, env, 2)
	playerCalls(t, env, "pw-play", 2)
}

func TestWatchSound_APlayerThatHangsIsKilledAfterTenSecondsAndNotedOnce(t *testing.T) {
	env, repo, p := soundEnv(t, soundConfig(`"message-new-instant"`))
	putSounds(env, "message-new-instant.oga")
	env.Stub("pw-play", batontest.WithScript("exec sleep 60"))
	w := startWatch(t, env)

	askForApproval(t, env, repo, p, "2031-03-04T10:00:05Z")
	playerCalls(t, env, "pw-play", 1)
	askForApproval(t, env, repo, p, "2031-03-04T10:00:15Z")
	playerCalls(t, env, "pw-play", 2)
	w.WaitStderr("killed")
	// The second player is killed a moment later, for the same reason; that
	// must not be a second line. Absence needs a short wait.
	time.Sleep(time.Second)
	res := w.Stop()

	warnings := warningLines(res.Stderr)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "pw-play") || !strings.Contains(warnings[0], "10s") {
		t.Errorf("warnings = %q, want one line about pw-play being killed after 10s", warnings)
	}
}

func TestWatchSound_APlayerThatFailsIsNotedOncePerMessage(t *testing.T) {
	env, repo, p := soundEnv(t, soundConfig(`"message-new-instant"`))
	putSounds(env, "message-new-instant.oga")
	env.Stub("pw-play", batontest.WithScript("echo 'cannot connect to the sound server' >&2; exit 1"))
	w := startWatch(t, env)

	askForApproval(t, env, repo, p, "2031-03-04T10:00:05Z")
	playerCalls(t, env, "pw-play", 1)
	w.WaitStderr("cannot connect to the sound server")
	askForApproval(t, env, repo, p, "2031-03-04T10:00:15Z")
	playerCalls(t, env, "pw-play", 2)
	// The second failure is reported, if it is going to be, a moment after
	// the call. Absence needs a short wait.
	time.Sleep(500 * time.Millisecond)
	res := w.Stop()

	warnings := warningLines(res.Stderr)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "pw-play") || !strings.Contains(warnings[0], "cannot connect to the sound server") {
		t.Errorf("warnings = %q, want one line for the failing player", warnings)
	}
}

func TestWatchSound_ATelegramMessageWhileAwayIsNotFollowedByASoundAndIsTheSameAsWithoutOne(t *testing.T) {
	bodies := map[string]string{}
	for name, table := range map[string]string{"without": "", "with": soundConfig(`"message-new-instant"`)} {
		t.Run(name, func(t *testing.T) {
			env, fake, repo, p := telegramEnv(t)
			env.WriteConfig(newConfig(env, telegramConfig(fake.server.URL)+table))
			putSounds(env, "message-new-instant.oga")
			hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
			awayAndWatch(t, env)

			askForApproval(t, env, repo, p, "2031-03-04T10:00:05Z")

			got := fake.waitFor(1)
			bodies[name] = got[0].Body
			if m := decodeSendMessage(t, got[0]); m.Text != "Baton: waiting for approval\nmy-app/fix-login (claude)" {
				t.Errorf("text = %q", m.Text)
			}
			if calls := env.Calls("notify-send"); len(calls) != 0 {
				t.Errorf("notify-send calls = %v, want none while away", calls)
			}
			if pw, pa := env.Calls("pw-play"), env.Calls("paplay"); len(pw)+len(pa) != 0 {
				t.Errorf("player calls = %v %v, want none for a Telegram message", pw, pa)
			}
		})
	}
	if bodies["with"] == "" || bodies["with"] != bodies["without"] {
		t.Errorf("request bodies differ: with %q, without %q", bodies["with"], bodies["without"])
	}
}

func TestWatchSound_AMessageThatFallsBackToTheDesktopFromAFailedTelegramRequestPlaysTheSound(t *testing.T) {
	env, fake, repo, p := telegramEnv(t)
	fake.answer(500, `{"ok":false,"error_code":500,"description":"Internal Server Error"}`)
	env.WriteConfig(newConfig(env, telegramConfig(fake.server.URL)+soundConfig(`"message-new-instant"`)))
	file := putSounds(env, "message-new-instant.oga")
	hostEvent(t, env, "s1", "2031-03-04T10:00:00Z", "SessionStart", repo, p)
	awayAndWatch(t, env)

	askForApproval(t, env, repo, p, "2031-03-04T10:00:05Z")

	if got, want := notifyArgs(notifications(t, env, 1)), [][]string{oldNotification}; !reflect.DeepEqual(got, want) {
		t.Errorf("notify-send calls = %q, want %q", got, want)
	}
	if got, want := playerCalls(t, env, "pw-play", 1), [][]string{{file}}; !reflect.DeepEqual(got, want) {
		t.Errorf("pw-play calls = %q, want %q", got, want)
	}
}
