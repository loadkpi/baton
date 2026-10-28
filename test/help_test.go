package test

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// helpCommands are the words that come before -h/--help, and the start of the
// usage line each one must print.
var helpCommands = []struct {
	words []string
	usage string
}{
	{[]string{"sync"}, "usage: baton sync"},
	{[]string{"new"}, "usage: baton new"},
	{[]string{"task"}, "usage: baton task"},
	{[]string{"task", "name"}, "usage: baton task name"},
	{[]string{"task", "status"}, "usage: baton task status"},
	{[]string{"session"}, "usage: baton session"},
	{[]string{"session", "end"}, "usage: baton session end"},
	{[]string{"handoff"}, "usage: baton handoff"},
	{[]string{"handoff", "save"}, "usage: baton handoff save"},
	{[]string{"hook"}, "usage: baton hook"},
	{[]string{"ls"}, "usage: baton ls"},
	{[]string{"prune"}, "usage: baton prune"},
	{[]string{"scrub"}, "usage: baton scrub"},
	{[]string{"watch"}, "usage: baton watch"},
	{[]string{"watch", "install"}, "usage: baton watch"},
	{[]string{"top"}, "usage: baton top"},
	{[]string{"away"}, "usage: baton away"},
	{[]string{"back"}, "usage: baton back"},
}

func TestHelp_EveryCommandPrintsItsUsageOnStdout(t *testing.T) {
	env := newEnv(t)

	for _, tc := range helpCommands {
		for _, flag := range []string{"-h", "--help"} {
			args := append(append([]string{}, tc.words...), flag)
			res := env.Run(args...)

			if res.ExitCode != 0 || res.Stderr != "" {
				t.Errorf("baton %v: exit code = %d, stderr=%q, want 0 and nothing on stderr", args, res.ExitCode, res.Stderr)
			}
			if !strings.HasPrefix(res.Stdout, tc.usage) {
				t.Errorf("baton %v: stdout = %q, want it to start with %q", args, res.Stdout, tc.usage)
			}
		}
	}
}

func TestHelp_ListsEveryCommandWithItsUsage(t *testing.T) {
	env := newEnv(t)

	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		res := env.Run(args...)

		if res.ExitCode != 0 || res.Stderr != "" {
			t.Errorf("baton %v: exit code = %d, stderr=%q, want 0 and nothing on stderr", args, res.ExitCode, res.Stderr)
		}
		for _, tc := range helpCommands {
			if !strings.Contains(res.Stdout, "\n"+tc.usage) && !strings.HasPrefix(res.Stdout, tc.usage) {
				t.Errorf("baton %v: stdout = %q, want a line starting with %q", args, res.Stdout, tc.usage)
			}
		}
	}
}

func TestHelp_NoCommandIsStillAUsageError(t *testing.T) {
	env := newEnv(t)

	res := env.Run()

	if res.ExitCode != 2 || !strings.HasPrefix(res.Stderr, "baton: a command is required") {
		t.Errorf("exit code = %d, stderr=%q, want 2 and the old error", res.ExitCode, res.Stderr)
	}
}

func TestHelp_HelpOfACommandIsTheCommandsOwnUsage(t *testing.T) {
	env := newEnv(t)

	res := env.Run("help", "ls")
	want := env.Run("ls", "--help")

	if res.ExitCode != 0 || res != want {
		t.Errorf("help ls = %+v, want %+v", res, want)
	}
}

// snapshot records the path, mode, size, mtime and content of everything
// under dir.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		content := ""
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			content = string(b)
		}
		out[p] = info.Mode().String() + " " + info.ModTime().Format(time.RFC3339Nano) + " " + content
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSync_HelpAndUnknownArgumentsTouchNothing(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/greet/SKILL.md", "---\nname: greet\ndescription: Say hello\n---\nHello.\n")
	env.CommitSource("add skills")
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	before := snapshot(t, env.Home)

	for _, tc := range []struct {
		args     []string
		exitCode int
	}{
		{[]string{"sync", "--help"}, 0},
		{[]string{"sync", "-h"}, 0},
		{[]string{"sync", "--bogus"}, 2},
		{[]string{"sync", "now"}, 2},
	} {
		res := env.Run(tc.args...)

		if res.ExitCode != tc.exitCode {
			t.Errorf("baton %v: exit code = %d, want %d; stderr=%q", tc.args, res.ExitCode, tc.exitCode, res.Stderr)
		}
		if tc.exitCode == 2 && res.Stderr != "baton: usage: baton sync\n" {
			t.Errorf("baton %v: stderr = %q, want the usage", tc.args, res.Stderr)
		}
		if after := snapshot(t, env.Home); !reflect.DeepEqual(after, before) {
			t.Fatalf("baton %v changed the home directory", tc.args)
		}
	}
}

func TestSync_UnknownArgumentIsRefusedBeforeTheConfigIsRead(t *testing.T) {
	env := newEnv(t)

	res := env.Run("sync", "--bogus")

	if res.ExitCode != 2 || res.Stderr != "baton: usage: baton sync\n" {
		t.Errorf("exit code = %d, stderr=%q, want the usage and no config error", res.ExitCode, res.Stderr)
	}
}

func TestHelp_AwayDoesNotTurnAwayModeOn(t *testing.T) {
	env := newEnv(t)
	before := snapshot(t, env.Home)

	for _, flag := range []string{"-h", "--help"} {
		env.Run("away", flag)
	}

	if after := snapshot(t, env.Home); !reflect.DeepEqual(after, before) {
		t.Errorf("away --help changed the home directory: %v", after)
	}
}

func TestHelp_TopDoesNotStartTheTable(t *testing.T) {
	env := newEnv(t)

	// Run on a terminal: without the help the table would take the screen.
	res := env.RunTerminal(nil, "top", "--help")

	if res.ExitCode != 0 || !strings.HasPrefix(res.Stdout, "usage: baton top") || strings.Contains(res.Stdout, "\x1b[?1049h") {
		t.Errorf("exit code = %d, stdout=%q, want the usage and no alternate screen", res.ExitCode, res.Stdout)
	}
}
