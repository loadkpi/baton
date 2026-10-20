package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/loadkpi/baton/internal/batontest"
)

func TestSync_MissingConfigFails(t *testing.T) {
	env := newEnv(t)

	res := env.Run("sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "config.toml") {
		t.Errorf("stderr = %q, want it to name the config file", res.Stderr)
	}
}

// standardConfig points Baton at the Env's source repository and configures
// one host Claude Profile, "claude-personal", in ~/.claude-personal.
func standardConfig(env *batontest.Env) string {
	return `source = "` + env.SourceDir + `"

[profiles.claude-personal]
agent = "claude"
dir = "` + env.Path(".claude-personal") + `"
`
}

func TestSync_CopiesSkillsIntoHostProfile(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/greet/SKILL.md", "---\nname: greet\ndescription: Say hello\n---\nHello.\n")
	env.SourceFile("skills/greet/scripts/hello.sh", "#!/bin/sh\necho hello\n", true)
	env.SourceFile("skills/review/SKILL.md", "---\nname: review\ndescription: Review code\n---\nReview.\n")
	env.CommitSource("add skills")
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := map[string]string{
		"skills/greet/SKILL.md":         "---\nname: greet\ndescription: Say hello\n---\nHello.\n",
		"skills/greet/scripts/hello.sh": "#!/bin/sh\necho hello\n",
		"skills/review/SKILL.md":        "---\nname: review\ndescription: Review code\n---\nReview.\n",
	}
	for rel, content := range want {
		if got := env.ReadFile(filepath.Join(profile, rel)); got != content {
			t.Errorf("%s = %q, want %q", rel, got, content)
		}
	}
	if mode := env.Mode(filepath.Join(profile, "skills/greet/scripts/hello.sh")); mode&0o111 == 0 {
		t.Errorf("hello.sh mode = %v, want it executable", mode)
	}
}

// manifestPaths returns the owned file paths recorded in a Profile's manifest.
func manifestPaths(t *testing.T, env *batontest.Env, profile string) []string {
	t.Helper()
	var m struct {
		Version int `json:"version"`
		Files   []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(env.ReadFile(".baton/manifests/"+profile+".json")), &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if m.Version != 1 {
		t.Errorf("manifest version = %d, want 1", m.Version)
	}
	paths := make([]string, 0, len(m.Files))
	for _, f := range m.Files {
		paths = append(paths, f.Path)
	}
	return paths
}

func TestSync_RecordsEveryWrittenFileInTheManifest(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.SourceFile("skills/greet/scripts/hello.sh", "echo hello\n", true)
	env.CommitSource("add skill")
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := []string{"skills/baton-handoff/SKILL.md", "skills/greet/SKILL.md", "skills/greet/scripts/hello.sh"}
	if got := manifestPaths(t, env, "claude-personal"); !slices.Equal(got, want) {
		t.Errorf("manifest files = %v, want %v", got, want)
	}
}

func TestSync_SecondRunWithoutSourceChangesChangesNothing(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.SourceFile("skills/greet/scripts/hello.sh", "echo hello\n", true)
	env.CommitSource("add skill")
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	files := []string{
		filepath.Join(profile, "skills/greet/SKILL.md"),
		filepath.Join(profile, "skills/greet/scripts/hello.sh"),
		env.Path(".baton/manifests/claude-personal.json"),
	}
	before := make(map[string]time.Time)
	for _, f := range files {
		before[f] = env.ModTime(f)
	}

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("second sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	for _, f := range files {
		if after := env.ModTime(f); !after.Equal(before[f]) {
			t.Errorf("%s was rewritten by the second sync", f)
		}
	}
}

func TestSync_InvalidConfigFailsClearlyAndWritesNothing(t *testing.T) {
	cases := []struct {
		name    string
		config  func(env *batontest.Env) string
		wantErr string
	}{
		{
			name:    "malformed TOML",
			config:  func(*batontest.Env) string { return "source = [" },
			wantErr: "config.toml",
		},
		{
			name: "missing source",
			config: func(env *batontest.Env) string {
				return "[profiles.p]\nagent = \"claude\"\ndir = \"" + env.Path(".claude-personal") + "\"\n"
			},
			wantErr: "source is required",
		},
		{
			name: "source directory does not exist",
			config: func(env *batontest.Env) string {
				return "source = \"" + env.Path("nope") + "\"\n[profiles.p]\nagent = \"claude\"\ndir = \"" + env.Path(".claude-personal") + "\"\n"
			},
			wantErr: "nope",
		},
		{
			name:    "no profiles",
			config:  func(env *batontest.Env) string { return "source = \"" + env.SourceDir + "\"\n" },
			wantErr: "no profiles",
		},
		{
			name: "unsupported agent",
			config: func(env *batontest.Env) string {
				return "source = \"" + env.SourceDir + "\"\n[profiles.p]\nagent = \"gemini\"\ndir = \"" + env.Path(".claude-personal") + "\"\n"
			},
			wantErr: "unsupported agent",
		},
		{
			name: "misspelled key",
			config: func(env *batontest.Env) string {
				return "source = \"" + env.SourceDir + "\"\n[profiles.p]\nagent = \"claude\"\ndirectory = \"" + env.Path(".claude-personal") + "\"\n"
			},
			wantErr: "directory",
		},
		{
			name: "profile directory does not exist",
			config: func(env *batontest.Env) string {
				return "source = \"" + env.SourceDir + "\"\n[profiles.p]\nagent = \"claude\"\ndir = \"" + env.Path(".claude-gone") + "\"\n"
			},
			wantErr: ".claude-gone",
		},
		{
			name: "relative profile directory",
			config: func(env *batontest.Env) string {
				return "source = \"" + env.SourceDir + "\"\n[profiles.p]\nagent = \"claude\"\ndir = \".claude-personal\"\n"
			},
			wantErr: "absolute",
		},
		{
			name: "invalid profile name",
			config: func(env *batontest.Env) string {
				return "source = \"" + env.SourceDir + "\"\n[profiles.\"../evil\"]\nagent = \"claude\"\ndir = \"" + env.Path(".claude-personal") + "\"\n"
			},
			wantErr: "invalid profile name",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newEnv(t)
			env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
			env.CommitSource("add skill")
			profile := env.MakeProfileDir(".claude-personal")
			env.WriteConfig(tc.config(env))

			res := env.Run("sync")

			if res.ExitCode == 0 {
				t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
			}
			if !strings.Contains(res.Stderr, tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", res.Stderr, tc.wantErr)
			}
			if env.Exists(filepath.Join(profile, "skills")) {
				t.Error("sync wrote into the Profile despite the invalid config")
			}
		})
	}
}

func TestSync_SourceWithoutSkillsDirectorySyncsNothing(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("AGENTS.md", "Be kind.\n")
	env.CommitSource("only instructions")
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := env.ListDir(filepath.Join(profile, "skills")); !slices.Equal(got, []string{"baton-handoff"}) {
		t.Errorf("skills/ = %v, want only the built-in skill although the source has no skills", got)
	}
}

func TestSync_SkipsDirectoryWithoutSkillMdAndWarns(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.SourceFile("skills/drafts/notes.txt", "not a skill yet\n")
	env.CommitSource("add skill and drafts")
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if !env.Exists(filepath.Join(profile, "skills/greet/SKILL.md")) {
		t.Error("the valid skill was not synced")
	}
	if env.Exists(filepath.Join(profile, "skills/drafts")) {
		t.Error("a directory without SKILL.md was synced")
	}
	if !strings.Contains(res.Stderr, "skills/drafts") {
		t.Errorf("stderr = %q, want a warning naming skills/drafts", res.Stderr)
	}
}

// foreignGreet sets up the situation of the question tests: the source has
// skills "greet" and "review", and the Profile already holds a hand-made skill
// "greet" (with an extra file) that Baton does not own. It returns the Profile.
func foreignGreet(t *testing.T, env *batontest.Env) string {
	t.Helper()
	env.SourceFile("skills/greet/SKILL.md", "Hello from the source.\n")
	env.SourceFile("skills/review/SKILL.md", "Review.\n")
	env.CommitSource("add skills")
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteFile(filepath.Join(profile, "skills/greet/SKILL.md"), "Hand-made greeting.\n")
	env.WriteFile(filepath.Join(profile, "skills/greet/notes.txt"), "my notes\n")
	env.WriteConfig(standardConfig(env))
	return profile
}

// onlyBackup returns the one timestamped directory of backups of a Profile.
func onlyBackup(t *testing.T, env *batontest.Env, profile string) string {
	t.Helper()
	names := env.ListDir(".baton/backups/" + profile)
	if len(names) != 1 {
		t.Fatalf("backups of %s = %v, want exactly one", profile, names)
	}
	return env.Path(".baton/backups", profile, names[0])
}

func TestSync_ForeignSkillWithoutTerminalStopsAndWritesNothing(t *testing.T) {
	env := newEnv(t)
	profile := foreignGreet(t, env)
	foreign := filepath.Join(profile, "skills/greet")

	res := env.RunWithStdin("y\n", "sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, foreign) || !strings.Contains(res.Stderr, "terminal") {
		t.Errorf("stderr = %q, want it to name %s and say there is no terminal", res.Stderr, foreign)
	}
	if got := env.ReadFile(filepath.Join(foreign, "SKILL.md")); got != "Hand-made greeting.\n" {
		t.Errorf("hand-made SKILL.md = %q, want it untouched", got)
	}
	if env.Exists(filepath.Join(profile, "skills/review")) {
		t.Error("sync wrote other files although it could not ask")
	}
	if env.Exists(".baton/manifests/claude-personal.json") || env.Exists(".baton/backups") {
		t.Error("sync recorded a manifest or a backup although it stopped")
	}
}

func TestSync_ForeignSkillIsShownAsDiffBackedUpAndReplacedOnYes(t *testing.T) {
	env := newEnv(t)
	profile := foreignGreet(t, env)

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	for _, want := range []string{
		filepath.Join(profile, "skills/greet"),
		"-Hand-made greeting.",
		"+Hello from the source.",
		"notes.txt",
	} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("transcript lacks %q:\n%s", want, res.Stdout)
		}
	}
	if got := env.ReadFile(filepath.Join(profile, "skills/greet/SKILL.md")); got != "Hello from the source.\n" {
		t.Errorf("SKILL.md = %q, want the source version", got)
	}
	if env.Exists(filepath.Join(profile, "skills/greet/notes.txt")) {
		t.Error("the foreign extra file is still in the replaced skill")
	}
	backup := onlyBackup(t, env, "claude-personal")
	if got := env.ReadFile(filepath.Join(backup, "skills/greet/SKILL.md")); got != "Hand-made greeting.\n" {
		t.Errorf("backed up SKILL.md = %q, want the hand-made version", got)
	}
	if got := env.ReadFile(filepath.Join(backup, "skills/greet/notes.txt")); got != "my notes\n" {
		t.Errorf("backed up notes.txt = %q, want the hand-made version", got)
	}
	want := []string{"skills/baton-handoff/SKILL.md", "skills/greet/SKILL.md", "skills/review/SKILL.md"}
	if got := manifestPaths(t, env, "claude-personal"); !slices.Equal(got, want) {
		t.Errorf("manifest files = %v, want %v", got, want)
	}
}

func TestSync_AnsweringNoStopsWithAnErrorNamingThePathAndWritesNothing(t *testing.T) {
	env := newEnv(t)
	profile := foreignGreet(t, env)
	foreign := filepath.Join(profile, "skills/greet")

	res := env.RunTerminal([]string{"n"}, "sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; transcript:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "baton: ") || strings.Count(res.Stdout, foreign) < 2 {
		t.Errorf("transcript = %q, want an error that names %s after the question", res.Stdout, foreign)
	}
	if got := env.ReadFile(filepath.Join(foreign, "SKILL.md")); got != "Hand-made greeting.\n" {
		t.Errorf("hand-made SKILL.md = %q, want it untouched", got)
	}
	if env.Exists(filepath.Join(profile, "skills/review")) {
		t.Error("sync wrote other files after the answer no")
	}
	if env.Exists(".baton/manifests/claude-personal.json") || env.Exists(".baton/backups") {
		t.Error("sync recorded a manifest or a backup after the answer no")
	}
}

func TestSync_UpdatesAFileItOwnsWhenTheSourceChanges(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.CommitSource("add skill")
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	env.SourceFile("skills/greet/SKILL.md", "Hello, world.\n")
	env.CommitSource("change skill")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("second sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := env.ReadFile(filepath.Join(profile, "skills/greet/SKILL.md")); got != "Hello, world.\n" {
		t.Errorf("SKILL.md = %q, want the changed source", got)
	}
}

func TestSync_RemovingASkillFromTheSourceRemovesItFromTheProfile(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.SourceFile("skills/greet/scripts/hello.sh", "echo hello\n", true)
	env.SourceFile("skills/review/SKILL.md", "Review.\n")
	env.CommitSource("add skills")
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteFile(filepath.Join(profile, "skills/synced/theirs/SKILL.md"), "Claude's own skill.\n")
	env.WriteConfig(standardConfig(env))
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	env.Command("git", "-C", env.SourceDir, "rm", "--quiet", "-r", "skills/greet")
	env.CommitSource("remove greet")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("second sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if env.Exists(filepath.Join(profile, "skills/greet")) {
		t.Error("skills/greet is still in the Profile")
	}
	if got := env.ReadFile(filepath.Join(profile, "skills/review/SKILL.md")); got != "Review.\n" {
		t.Errorf("review/SKILL.md = %q, want it kept", got)
	}
	if got := env.ReadFile(filepath.Join(profile, "skills/synced/theirs/SKILL.md")); got != "Claude's own skill.\n" {
		t.Errorf("foreign skill = %q, want it untouched", got)
	}
	if got, want := manifestPaths(t, env, "claude-personal"), []string{"skills/baton-handoff/SKILL.md", "skills/review/SKILL.md"}; !slices.Equal(got, want) {
		t.Errorf("manifest files = %v, want %v", got, want)
	}
}

// gitInit makes dir a git repository and commits everything in it. It uses the
// Env's git, never the developer's.
func gitInit(t *testing.T, env *batontest.Env, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"-C", dir, "init", "--quiet", "--initial-branch=main"},
		{"-C", dir, "add", "--all", "--force"},
		{"-C", dir, "commit", "--quiet", "--allow-empty", "-m", "profile"},
	} {
		if res := env.Command("git", args...); res.ExitCode != 0 {
			t.Fatalf("git %v: %s", args, res.Stderr)
		}
	}
}

func TestSync_NeverOverwritesAForeignFileThatGitTracks(t *testing.T) {
	env := newEnv(t)
	profile := foreignGreet(t, env)
	gitInit(t, env, profile)
	tracked := filepath.Join(profile, "skills/greet")

	res := env.RunTerminal(nil, "sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; transcript:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, tracked) || !strings.Contains(res.Stdout, "git") {
		t.Errorf("transcript = %q, want an error that names %s and git", res.Stdout, tracked)
	}
	if strings.Contains(res.Stdout, "[y/N]") {
		t.Errorf("sync asked about a file it may never change:\n%s", res.Stdout)
	}
	if got := env.ReadFile(filepath.Join(tracked, "SKILL.md")); got != "Hand-made greeting.\n" {
		t.Errorf("tracked SKILL.md = %q, want it untouched", got)
	}
	if env.Exists(filepath.Join(profile, "skills/review")) || env.Exists(".baton") {
		t.Error("sync wrote something although it stopped")
	}
}

func TestSync_NeverChangesAnOwnedFileThatGitTracks(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.CommitSource("add skill")
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	gitInit(t, env, profile) // the Profile is a repository that tracks the synced skill
	skill := filepath.Join(profile, "skills/greet/SKILL.md")

	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Errorf("sync with nothing to change: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	env.SourceFile("skills/greet/SKILL.md", "Hello, world.\n")
	env.CommitSource("change skill")
	res := env.Run("sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, skill) || !strings.Contains(res.Stderr, "git") {
		t.Errorf("stderr = %q, want it to name %s and git", res.Stderr, skill)
	}
	if got := env.ReadFile(skill); got != "Hello.\n" {
		t.Errorf("tracked SKILL.md = %q, want it untouched", got)
	}
}

func TestSync_WritesUntrackedFilesInAProfileThatIsAGitRepository(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.CommitSource("add skill")
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteFile(filepath.Join(profile, "keybindings.json"), "{}\n")
	gitInit(t, env, profile)
	env.WriteConfig(standardConfig(env))

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := env.ReadFile(filepath.Join(profile, "skills/greet/SKILL.md")); got != "Hello.\n" {
		t.Errorf("SKILL.md = %q, want the source version", got)
	}
}

func TestSync_NeverTouchesWhatIsNotInTheManifest(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.CommitSource("add skill")
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteFile(filepath.Join(profile, "keybindings.json"), `{"theme": "dark"}`+"\n")
	env.WriteFile(filepath.Join(profile, "skills/third-party/SKILL.md"), "Installed by someone else.\n")
	env.WriteConfig(standardConfig(env))
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	// The user adds a file of their own to a skill that Baton owns.
	mine := filepath.Join(profile, "skills/greet/mine.txt")
	env.WriteFile(mine, "my own file\n")
	untouched := []string{
		filepath.Join(profile, "keybindings.json"),
		filepath.Join(profile, "skills/third-party/SKILL.md"),
		mine,
	}
	before := make(map[string]time.Time)
	for _, f := range untouched {
		before[f] = env.ModTime(f)
	}

	env.SourceFile("skills/greet/SKILL.md", "Hello, world.\n")
	env.CommitSource("change skill")
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("sync after a change: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	env.Command("git", "-C", env.SourceDir, "rm", "--quiet", "-r", "skills/greet")
	env.CommitSource("remove skill")
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("sync after a removal: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	if env.Exists(filepath.Join(profile, "skills/greet/SKILL.md")) {
		t.Error("the owned SKILL.md is still there after the skill left the source")
	}
	for _, f := range untouched {
		if after := env.ModTime(f); !after.Equal(before[f]) {
			t.Errorf("%s was modified by sync", f)
		}
	}
	if got := env.ReadFile(mine); got != "my own file\n" {
		t.Errorf("mine.txt = %q, want it kept", got)
	}
}

func TestSync_ForeignFileInsideAnOwnedSkillGetsTheSameQuestion(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.CommitSource("add skill")
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	// The source grows a file at a path where the user already keeps a file.
	notes := filepath.Join(profile, "skills/greet/notes.md")
	env.WriteFile(notes, "my notes\n")
	env.SourceFile("skills/greet/notes.md", "Source notes.\n")
	env.CommitSource("add notes")

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "-my notes") || !strings.Contains(res.Stdout, "+Source notes.") {
		t.Errorf("transcript lacks the diff:\n%s", res.Stdout)
	}
	if got := env.ReadFile(notes); got != "Source notes.\n" {
		t.Errorf("notes.md = %q, want the source version", got)
	}
	if got := env.ReadFile(filepath.Join(onlyBackup(t, env, "claude-personal"), "skills/greet/notes.md")); got != "my notes\n" {
		t.Errorf("backed up notes.md = %q, want the hand-made version", got)
	}
}

func TestSync_ForeignSkillThatIsASymlinkIsReplacedButItsTargetIsNot(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/greet/SKILL.md", "Hello from the source.\n")
	env.CommitSource("add skill")
	profile := env.MakeProfileDir(".claude-personal")
	// A third-party installer keeps skills elsewhere and links them in.
	installed := env.Path(".agents/skills/greet")
	env.WriteFile(filepath.Join(installed, "SKILL.md"), "Installed greeting.\n")
	if err := os.MkdirAll(filepath.Join(profile, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(installed, filepath.Join(profile, "skills/greet")); err != nil {
		t.Fatal(err)
	}
	env.WriteConfig(standardConfig(env))

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "symbolic link") || !strings.Contains(res.Stdout, installed) {
		t.Errorf("transcript = %q, want it to say that the skill is a link to %s", res.Stdout, installed)
	}
	if got := env.ReadFile(filepath.Join(installed, "SKILL.md")); got != "Installed greeting.\n" {
		t.Errorf("the link target = %q, want it untouched", got)
	}
	if got := env.ReadFile(filepath.Join(profile, "skills/greet/SKILL.md")); got != "Hello from the source.\n" {
		t.Errorf("SKILL.md = %q, want the source version", got)
	}
	if info, err := os.Lstat(filepath.Join(profile, "skills/greet")); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("skills/greet should be a real directory now, got %v (err %v)", info, err)
	}
	backup := filepath.Join(onlyBackup(t, env, "claude-personal"), "skills/greet")
	if target, err := os.Readlink(backup); err != nil || target != installed {
		t.Errorf("backup of the link = %q (err %v), want a link to %s", target, err, installed)
	}
}

func TestSync_StopsOnTheSecondQuestionWithoutWritingAnything(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/greet/SKILL.md", "Hello from the source.\n")
	env.SourceFile("skills/review/SKILL.md", "Review from the source.\n")
	env.CommitSource("add skills")
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteFile(filepath.Join(profile, "skills/greet/SKILL.md"), "Hand-made greeting.\n")
	env.WriteFile(filepath.Join(profile, "skills/review/SKILL.md"), "Hand-made review.\n")
	env.WriteConfig(standardConfig(env))

	res := env.RunTerminal([]string{"y", "n"}, "sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; transcript:\n%s", res.Stdout)
	}
	for skill, want := range map[string]string{"greet": "Hand-made greeting.\n", "review": "Hand-made review.\n"} {
		if got := env.ReadFile(filepath.Join(profile, "skills", skill, "SKILL.md")); got != want {
			t.Errorf("%s/SKILL.md = %q, want it untouched", skill, got)
		}
	}
	if env.Exists(".baton/manifests/claude-personal.json") || env.Exists(".baton/backups") {
		t.Error("sync recorded a manifest or a backup although it stopped")
	}
}

func TestSync_ManifestPathThatLeavesTheProfileIsRefused(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.CommitSource("add skill")
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	outside := env.Path("precious.txt")
	env.WriteFile(outside, "do not delete\n")
	env.WriteFile(".baton/manifests/claude-personal.json",
		`{"version": 1, "profile": "claude-personal", "files": [{"path": "../precious.txt"}]}`+"\n")

	res := env.Run("sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "../precious.txt") {
		t.Errorf("stderr = %q, want it to name the bad manifest path", res.Stderr)
	}
	if got := env.ReadFile(outside); got != "do not delete\n" {
		t.Errorf("precious.txt = %q, want it untouched", got)
	}
}

const (
	blockBegin = "<!-- baton:begin -->"
	blockEnd   = "<!-- baton:end -->"
)

// block is how Baton's part of a Markdown file looks around body.
func block(body string) string {
	return blockBegin + "\n" + body + blockEnd + "\n"
}

// manifestBlocks returns the owned blocks recorded in a Profile's manifest, as
// path to checksum.
func manifestBlocks(t *testing.T, env *batontest.Env, profile string) map[string]string {
	t.Helper()
	var m struct {
		Blocks []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal([]byte(env.ReadFile(".baton/manifests/"+profile+".json")), &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	blocks := make(map[string]string)
	for _, b := range m.Blocks {
		blocks[b.Path] = b.SHA256
	}
	return blocks
}

// withInstructions sets up the block tests: the source holds AGENTS.md and one
// skill, and the Profile exists. It returns the Profile.
func withInstructions(env *batontest.Env, agents string) string {
	env.SourceFile("AGENTS.md", agents)
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.CommitSource("add instructions")
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	return profile
}

func TestSync_CreatesInstructionsFileWithBatonsBlockWhenThereIsNone(t *testing.T) {
	env := newEnv(t)
	profile := withInstructions(env, "Be kind.\n")
	claudeMd := filepath.Join(profile, "CLAUDE.md")

	res := env.Run("sync") // no terminal: a new file is nobody's, so nothing to ask

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got, want := env.ReadFile(claudeMd), "<!-- baton:begin -->\nBe kind.\n<!-- baton:end -->\n"; got != want {
		t.Errorf("CLAUDE.md = %q, want %q", got, want)
	}
	sum, ok := manifestBlocks(t, env, "claude-personal")["CLAUDE.md"]
	if !ok || len(sum) != 64 {
		t.Errorf("manifest blocks = %v, want a 64-digit checksum for CLAUDE.md", manifestBlocks(t, env, "claude-personal"))
	}

	before := env.ModTime(claudeMd)
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("second sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if !env.ModTime(claudeMd).Equal(before) {
		t.Error("the second sync rewrote CLAUDE.md")
	}
}

func TestSync_ExistingInstructionsFileWithoutTerminalStopsAndWritesNothing(t *testing.T) {
	env := newEnv(t)
	profile := withInstructions(env, "Be kind.\n")
	claudeMd := filepath.Join(profile, "CLAUDE.md")
	env.WriteFile(claudeMd, "My rules.\n")

	res := env.RunWithStdin("y\n", "sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, claudeMd) || !strings.Contains(res.Stderr, "terminal") {
		t.Errorf("stderr = %q, want it to name %s and say there is no terminal", res.Stderr, claudeMd)
	}
	if got := env.ReadFile(claudeMd); got != "My rules.\n" {
		t.Errorf("CLAUDE.md = %q, want it untouched", got)
	}
	if env.Exists(filepath.Join(profile, "skills")) || env.Exists(".baton") {
		t.Error("sync wrote something although it could not ask")
	}
}

func TestSync_AddsBlockToExistingInstructionsFileAfterDiffAndBackup(t *testing.T) {
	env := newEnv(t)
	profile := withInstructions(env, "Be kind.\n")
	claudeMd := filepath.Join(profile, "CLAUDE.md")
	env.WriteFile(claudeMd, "My rules.\n")

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	for _, want := range []string{claudeMd, "+" + blockBegin, "+Be kind.", "+" + blockEnd} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("transcript lacks %q:\n%s", want, res.Stdout)
		}
	}
	if got, want := env.ReadFile(claudeMd), "My rules.\n\n"+block("Be kind.\n"); got != want {
		t.Errorf("CLAUDE.md = %q, want %q", got, want)
	}
	if got := env.ReadFile(filepath.Join(onlyBackup(t, env, "claude-personal"), "CLAUDE.md")); got != "My rules.\n" {
		t.Errorf("backed up CLAUDE.md = %q, want the original", got)
	}
}

func TestSync_AnsweringNoLeavesInstructionsFileAlone(t *testing.T) {
	env := newEnv(t)
	profile := withInstructions(env, "Be kind.\n")
	claudeMd := filepath.Join(profile, "CLAUDE.md")
	env.WriteFile(claudeMd, "My rules.\n")

	res := env.RunTerminal([]string{"n"}, "sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; transcript:\n%s", res.Stdout)
	}
	if got := env.ReadFile(claudeMd); got != "My rules.\n" {
		t.Errorf("CLAUDE.md = %q, want it untouched", got)
	}
	if env.Exists(filepath.Join(profile, "skills")) || env.Exists(".baton/manifests") || env.Exists(".baton/backups") {
		t.Error("sync wrote something after the answer no")
	}
}

func TestSync_LaterSyncsUpdateTheBlockWithoutAskingAndKeepTheRestByteForByte(t *testing.T) {
	env := newEnv(t)
	profile := withInstructions(env, "Be kind.\n")
	claudeMd := filepath.Join(profile, "CLAUDE.md")
	env.WriteFile(claudeMd, "My rules.\n")
	if res := env.RunTerminal([]string{"y"}, "sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	// The user keeps editing around Baton's block.
	env.WriteFile(claudeMd, "Top rules\n\r\n"+block("Be kind.\n")+"\nBottom rules, no newline at the end")
	env.SourceFile("AGENTS.md", "Be kind.\nBe brief.\n")
	env.CommitSource("change instructions")

	res := env.Run("sync") // no terminal: Baton's own part is updated silently

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := "Top rules\n\r\n" + block("Be kind.\nBe brief.\n") + "\nBottom rules, no newline at the end"
	if got := env.ReadFile(claudeMd); got != want {
		t.Errorf("CLAUDE.md = %q, want %q", got, want)
	}
	if got := len(env.ListDir(".baton/backups/claude-personal")); got != 1 {
		t.Errorf("%d backups, want only the one from the first change", got)
	}
}

func TestSync_AsksAgainWhenBatonsBlockWasEditedByHand(t *testing.T) {
	env := newEnv(t)
	profile := withInstructions(env, "Be kind.\n")
	claudeMd := filepath.Join(profile, "CLAUDE.md")
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	edited := block("Be kind, my way.\n")
	env.WriteFile(claudeMd, edited)

	res := env.Run("sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, claudeMd) || !strings.Contains(res.Stderr, "terminal") {
		t.Errorf("stderr = %q, want it to name %s and say there is no terminal", res.Stderr, claudeMd)
	}
	if got := env.ReadFile(claudeMd); got != edited {
		t.Errorf("CLAUDE.md = %q, want the hand-edited block kept", got)
	}

	answered := env.RunTerminal([]string{"y"}, "sync")

	if answered.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", answered.ExitCode, answered.Stdout)
	}
	if !strings.Contains(answered.Stdout, "-Be kind, my way.") || !strings.Contains(answered.Stdout, "+Be kind.") {
		t.Errorf("transcript lacks the diff:\n%s", answered.Stdout)
	}
	if got := env.ReadFile(claudeMd); got != block("Be kind.\n") {
		t.Errorf("CLAUDE.md = %q, want Baton's block restored", got)
	}
	backups := env.ListDir(".baton/backups/claude-personal")
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want one", backups)
	}
	if got := env.ReadFile(filepath.Join(onlyBackup(t, env, "claude-personal"), "CLAUDE.md")); got != edited {
		t.Errorf("backed up CLAUDE.md = %q, want the hand-edited version", got)
	}
}

func TestSync_RemovingInstructionsFromTheSourceTakesOnlyTheBlockOut(t *testing.T) {
	env := newEnv(t)
	profile := withInstructions(env, "Be kind.\n")
	claudeMd := filepath.Join(profile, "CLAUDE.md")
	env.WriteFile(claudeMd, "My rules.\n")
	if res := env.RunTerminal([]string{"y"}, "sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	env.Command("git", "-C", env.SourceDir, "rm", "--quiet", "AGENTS.md")
	env.CommitSource("drop instructions")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := env.ReadFile(claudeMd); got != "My rules.\n" {
		t.Errorf("CLAUDE.md = %q, want the original text back", got)
	}
	if blocks := manifestBlocks(t, env, "claude-personal"); len(blocks) != 0 {
		t.Errorf("manifest blocks = %v, want none", blocks)
	}
}

func TestSync_RemovingInstructionsDeletesTheFileSyncCreated(t *testing.T) {
	env := newEnv(t)
	profile := withInstructions(env, "Be kind.\n")
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	env.Command("git", "-C", env.SourceDir, "rm", "--quiet", "AGENTS.md")
	env.CommitSource("drop instructions")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if env.Exists(filepath.Join(profile, "CLAUDE.md")) {
		t.Error("CLAUDE.md is still there although only Baton's block was in it")
	}
}

func TestSync_BrokenBlockMarkersStopSyncAndNameTheFile(t *testing.T) {
	env := newEnv(t)
	profile := withInstructions(env, "Be kind.\n")
	claudeMd := filepath.Join(profile, "CLAUDE.md")
	broken := "Mine.\n" + blockBegin + "\nhalf a block\n"
	env.WriteFile(claudeMd, broken)

	res := env.RunTerminal(nil, "sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; transcript:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, claudeMd) || strings.Contains(res.Stdout, "[y/N]") {
		t.Errorf("transcript = %q, want an error naming %s and no question", res.Stdout, claudeMd)
	}
	if got := env.ReadFile(claudeMd); got != broken {
		t.Errorf("CLAUDE.md = %q, want it untouched", got)
	}
}

func TestSync_NeverWritesAnInstructionsFileThatGitTracks(t *testing.T) {
	env := newEnv(t)
	profile := withInstructions(env, "Be kind.\n")
	claudeMd := filepath.Join(profile, "CLAUDE.md")
	env.WriteFile(claudeMd, "My rules.\n")
	gitInit(t, env, profile)

	res := env.RunTerminal(nil, "sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; transcript:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, claudeMd) || strings.Contains(res.Stdout, "[y/N]") {
		t.Errorf("transcript = %q, want an error naming %s and no question", res.Stdout, claudeMd)
	}
	if got := env.ReadFile(claudeMd); got != "My rules.\n" {
		t.Errorf("CLAUDE.md = %q, want it untouched", got)
	}
}

func TestSync_InstructionsFileThatIsASymlinkKeepsTheLinkAndChangesItsTarget(t *testing.T) {
	env := newEnv(t)
	profile := withInstructions(env, "Be kind.\n")
	shared := env.Path("dotfiles/claude.md")
	env.WriteFile(shared, "My rules.\n")
	if err := os.Symlink(shared, filepath.Join(profile, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	if target, err := os.Readlink(filepath.Join(profile, "CLAUDE.md")); err != nil || target != shared {
		t.Errorf("CLAUDE.md link = %q (err %v), want it kept, pointing to %s", target, err, shared)
	}
	if got, want := env.ReadFile(shared), "My rules.\n\n"+block("Be kind.\n"); got != want {
		t.Errorf("link target = %q, want %q", got, want)
	}
}

func TestSync_QuestionShowsAUnifiedDiffWithContext(t *testing.T) {
	env := newEnv(t)
	profile := withInstructions(env, "Be kind.\n")
	var lines strings.Builder
	for i := 1; i <= 10; i++ {
		lines.WriteString("line " + strconv.Itoa(i) + "\n")
	}
	env.WriteFile(filepath.Join(profile, "CLAUDE.md"), lines.String())

	res := env.RunTerminal([]string{"n"}, "sync")

	want := "--- a/CLAUDE.md\n+++ b/CLAUDE.md\n@@ -8,3 +8,7 @@\n line 8\n line 9\n line 10\n+\n+<!-- baton:begin -->\n+Be kind.\n+<!-- baton:end -->\n"
	if !strings.Contains(res.Stdout, want) {
		t.Errorf("transcript lacks the diff %q:\n%s", want, res.Stdout)
	}
}

// twoProfilesConfig configures a personal and a work host Claude Profile with
// the tags each accepts (a TOML array literal such as `["work"]`, or "" for
// none).
func twoProfilesConfig(env *batontest.Env, personalTags, workTags string) string {
	tagsLine := func(tags string) string {
		if tags == "" {
			return ""
		}
		return "tags = " + tags + "\n"
	}
	return `source = "` + env.SourceDir + `"

[profiles.claude-personal]
agent = "claude"
dir = "` + env.Path(".claude-personal") + `"
` + tagsLine(personalTags) + `
[profiles.claude-work]
agent = "claude"
dir = "` + env.Path(".claude-work") + `"
` + tagsLine(workTags)
}

func TestSync_ATaggedSkillReachesOnlyProfilesThatAcceptItsTag(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/common/SKILL.md", "---\nname: common\n---\nFor everyone.\n")
	env.SourceFile("skills/deploy/SKILL.md", "---\nname: deploy\ntags: [work]\n---\nWork only.\n")
	env.SourceFile("skills/deploy/scripts/run.sh", "echo deploy\n", true)
	env.SourceFile("skills/journal/SKILL.md", "---\nname: journal\ntags: [personal]\n---\nPersonal only.\n")
	env.CommitSource("add skills")
	personal := env.MakeProfileDir(".claude-personal")
	work := env.MakeProfileDir(".claude-work")
	env.WriteConfig(twoProfilesConfig(env, `["personal"]`, `["work"]`))

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	for _, c := range []struct {
		profile string
		rel     string
		want    bool
	}{
		{personal, "skills/common/SKILL.md", true},
		{work, "skills/common/SKILL.md", true},
		{work, "skills/deploy/SKILL.md", true},
		{work, "skills/deploy/scripts/run.sh", true},
		{personal, "skills/deploy", false},
		{personal, "skills/journal/SKILL.md", true},
		{work, "skills/journal", false},
	} {
		if got := env.Exists(filepath.Join(c.profile, c.rel)); got != c.want {
			t.Errorf("%s exists in %s = %v, want %v", c.rel, c.profile, got, c.want)
		}
	}
	if got, want := manifestPaths(t, env, "claude-personal"), []string{"skills/baton-handoff/SKILL.md", "skills/common/SKILL.md", "skills/journal/SKILL.md"}; !slices.Equal(got, want) {
		t.Errorf("personal manifest files = %v, want %v", got, want)
	}
}

func TestSync_ChangingAProfilesTagsRemovesWhatItNoLongerAcceptsAndOnlyThat(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/common/SKILL.md", "For everyone.\n")
	env.SourceFile("skills/deploy/SKILL.md", "---\ntags: [work]\n---\nWork only.\n")
	env.SourceFile("skills/deploy/scripts/run.sh", "echo deploy\n", true)
	env.CommitSource("add skills")
	env.MakeProfileDir(".claude-personal")
	work := env.MakeProfileDir(".claude-work")
	env.WriteFile(filepath.Join(work, "skills/theirs/SKILL.md"), "Not Baton's.\n")
	env.WriteConfig(twoProfilesConfig(env, "", `["work"]`))
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if !env.Exists(filepath.Join(work, "skills/deploy/SKILL.md")) {
		t.Fatal("setup: the work Profile did not get the deploy skill")
	}

	env.WriteConfig(twoProfilesConfig(env, "", `["personal"]`))
	res := env.Run("sync") // no terminal: removing what Baton owns needs no question

	if res.ExitCode != 0 {
		t.Fatalf("second sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if env.Exists(filepath.Join(work, "skills/deploy")) {
		t.Error("skills/deploy is still in the work Profile after it stopped accepting work")
	}
	if got := env.ReadFile(filepath.Join(work, "skills/common/SKILL.md")); got != "For everyone.\n" {
		t.Errorf("common/SKILL.md = %q, want it kept", got)
	}
	if got := env.ReadFile(filepath.Join(work, "skills/theirs/SKILL.md")); got != "Not Baton's.\n" {
		t.Errorf("foreign skill = %q, want it untouched", got)
	}
	if got, want := manifestPaths(t, env, "claude-work"), []string{"skills/baton-handoff/SKILL.md", "skills/common/SKILL.md"}; !slices.Equal(got, want) {
		t.Errorf("manifest files = %v, want %v", got, want)
	}

	env.WriteConfig(twoProfilesConfig(env, "", `["work", "personal"]`))
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("third sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := env.ReadFile(filepath.Join(work, "skills/deploy/SKILL.md")); got != "---\ntags: [work]\n---\nWork only.\n" {
		t.Errorf("deploy/SKILL.md = %q, want it back once work is accepted again", got)
	}
}

func TestSync_TagsInTheFrontMatterCanBeAScalarOrABlockList(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/one/SKILL.md", "---\nname: one\ndescription: >\n  tags: personal\ntags: work   # only here\n---\nOne.\n")
	env.SourceFile("skills/two/SKILL.md", "---\ntags:\n  - 'personal'\n  - \"other\"\nname: two\n---\nTwo.\n")
	env.SourceFile("skills/three/SKILL.md", "---\r\ntags: [work]\r\n---\r\nThree.\r\n")
	env.CommitSource("add skills")
	personal := env.MakeProfileDir(".claude-personal")
	work := env.MakeProfileDir(".claude-work")
	env.WriteConfig(twoProfilesConfig(env, `["personal"]`, `["work"]`))

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	for _, c := range []struct {
		profile, skill string
		want           bool
	}{
		{work, "one", true}, {personal, "one", false},
		{personal, "two", true}, {work, "two", false},
		{work, "three", true}, {personal, "three", false},
	} {
		if got := env.Exists(filepath.Join(c.profile, "skills", c.skill)); got != c.want {
			t.Errorf("skill %s in %s = %v, want %v", c.skill, c.profile, got, c.want)
		}
	}
}

func TestSync_AProfileWithoutTagsGetsOnlyUntaggedSkills(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/common/SKILL.md", "---\nname: common\n---\nFor everyone.\n")
	env.SourceFile("skills/deploy/SKILL.md", "---\ntags: [work]\n---\nWork only.\n")
	env.CommitSource("add skills")
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if !env.Exists(filepath.Join(profile, "skills/common/SKILL.md")) {
		t.Error("the untagged skill did not reach a Profile that accepts no tags")
	}
	if env.Exists(filepath.Join(profile, "skills/deploy")) {
		t.Error("a work skill reached a Profile that accepts no tags")
	}
}

func TestSync_UnreadableTagsStopSyncBeforeAnythingIsWritten(t *testing.T) {
	cases := []struct {
		name, skillMd, wantErr string
	}{
		{"invalid tag", "---\ntags: [Work]\n---\nx\n", "invalid tag \"Work\""},
		{"empty list", "---\ntags: []\n---\nx\n", "list is empty"},
		{"front matter not closed", "---\ntags: [work]\nx\n", "not closed"},
		{"tags twice", "---\ntags: [work]\ntags: [personal]\n---\nx\n", "twice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newEnv(t)
			env.SourceFile("skills/common/SKILL.md", "For everyone.\n")
			env.SourceFile("skills/bad/SKILL.md", tc.skillMd)
			env.CommitSource("add skills")
			profile := env.MakeProfileDir(".claude-personal")
			env.WriteConfig(standardConfig(env))

			res := env.Run("sync")

			if res.ExitCode == 0 {
				t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
			}
			for _, want := range []string{"skills/bad/SKILL.md", tc.wantErr} {
				if !strings.Contains(res.Stderr, want) {
					t.Errorf("stderr = %q, want it to contain %q", res.Stderr, want)
				}
			}
			if env.Exists(filepath.Join(profile, "skills")) {
				t.Error("sync wrote skills although a skill's tags could not be read")
			}
		})
	}
}

func TestSync_InvalidProfileTagsFailClearly(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/common/SKILL.md", "For everyone.\n")
	env.CommitSource("add skill")
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env) + "tags = [\"Work!\"]\n")

	res := env.Run("sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "claude-personal") || !strings.Contains(res.Stderr, "Work!") {
		t.Errorf("stderr = %q, want it to name the Profile and the tag", res.Stderr)
	}
}

// taggedAgents is an AGENTS.md with a part for everyone and parts for one tag.
const taggedAgents = `# Rules

Be kind.

<!-- baton:tags work -->
## Work

Use the ticket tracker.

<!-- /baton:tags -->
<!-- baton:tags personal, family -->
## Home

Keep it short.
<!-- /baton:tags -->

Be brief.
`

func TestSync_TaggedPartsOfTheInstructionsReachOnlyProfilesThatAcceptThem(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("AGENTS.md", taggedAgents)
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.CommitSource("add instructions")
	personal := env.MakeProfileDir(".claude-personal")
	work := env.MakeProfileDir(".claude-work")
	env.WriteConfig(twoProfilesConfig(env, `["family"]`, `["work"]`))

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	wantWork := block("# Rules\n\nBe kind.\n\n## Work\n\nUse the ticket tracker.\n\nBe brief.\n")
	if got := env.ReadFile(filepath.Join(work, "CLAUDE.md")); got != wantWork {
		t.Errorf("work CLAUDE.md = %q, want %q", got, wantWork)
	}
	wantPersonal := block("# Rules\n\nBe kind.\n\n## Home\n\nKeep it short.\n\nBe brief.\n")
	if got := env.ReadFile(filepath.Join(personal, "CLAUDE.md")); got != wantPersonal {
		t.Errorf("personal CLAUDE.md = %q, want %q", got, wantPersonal)
	}
}

func TestSync_ChangingAProfilesTagsUpdatesItsInstructionsBlockWithoutAsking(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("AGENTS.md", taggedAgents)
	env.CommitSource("add instructions")
	env.MakeProfileDir(".claude-personal")
	work := env.MakeProfileDir(".claude-work")
	claudeMd := filepath.Join(work, "CLAUDE.md")
	env.WriteFile(claudeMd, "My rules.\n")
	env.WriteConfig(twoProfilesConfig(env, "", `["work"]`))
	if res := env.RunTerminal([]string{"y"}, "sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}

	env.WriteConfig(twoProfilesConfig(env, "", `["family"]`))
	res := env.Run("sync") // no terminal: Baton's own block is updated silently

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := "My rules.\n\n" + block("# Rules\n\nBe kind.\n\n## Home\n\nKeep it short.\n\nBe brief.\n")
	if got := env.ReadFile(claudeMd); got != want {
		t.Errorf("CLAUDE.md = %q, want %q", got, want)
	}
}

func TestSync_InstructionsThatLeaveNothingForAProfileGiveItNoBlock(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("AGENTS.md", "<!-- baton:tags work -->\nWork only.\n<!-- /baton:tags -->\n")
	env.CommitSource("add instructions")
	personal := env.MakeProfileDir(".claude-personal")
	work := env.MakeProfileDir(".claude-work")
	env.WriteConfig(twoProfilesConfig(env, "", `["work"]`))

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if env.Exists(filepath.Join(personal, "CLAUDE.md")) {
		t.Error("a Profile that accepts none of the tags got a CLAUDE.md")
	}
	if got, want := env.ReadFile(filepath.Join(work, "CLAUDE.md")), block("Work only.\n"); got != want {
		t.Errorf("work CLAUDE.md = %q, want %q", got, want)
	}

	// Once the work Profile stops accepting work, its block goes and the file
	// Baton created goes with it.
	env.WriteConfig(twoProfilesConfig(env, "", ""))
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("second sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if env.Exists(filepath.Join(work, "CLAUDE.md")) {
		t.Error("CLAUDE.md that held only Baton's block is still there")
	}
	if got := manifestBlocks(t, env, "claude-work"); len(got) != 0 {
		t.Errorf("manifest blocks = %v, want none", got)
	}
}

func TestSync_BrokenTagPartsInTheInstructionsStopSyncAndSayWhere(t *testing.T) {
	cases := []struct {
		name, agents, wantErr string
	}{
		{"never closed", "Intro.\n<!-- baton:tags work -->\nText.\n", "line 2: the tags part is never closed"},
		{"closed twice", "<!-- baton:tags work -->\nText.\n<!-- /baton:tags -->\n<!-- /baton:tags -->\n", "line 4"},
		{"nested", "<!-- baton:tags work -->\n<!-- baton:tags personal -->\n", "line 2: a tags part inside"},
		{"no tags", "<!-- baton:tags  -->\nText.\n<!-- /baton:tags -->\n", "line 1: a tags part needs at least one tag"},
		{"invalid tag", "<!-- baton:tags Work -->\nText.\n<!-- /baton:tags -->\n", "line 1: invalid tag \"Work\""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newEnv(t)
			env.SourceFile("AGENTS.md", tc.agents)
			env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
			env.CommitSource("add instructions")
			profile := env.MakeProfileDir(".claude-personal")
			env.WriteConfig(standardConfig(env))

			res := env.Run("sync")

			if res.ExitCode == 0 {
				t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
			}
			if !strings.Contains(res.Stderr, "AGENTS.md") || !strings.Contains(res.Stderr, tc.wantErr) {
				t.Errorf("stderr = %q, want it to name AGENTS.md and contain %q", res.Stderr, tc.wantErr)
			}
			if env.Exists(filepath.Join(profile, "skills")) || env.Exists(filepath.Join(profile, "CLAUDE.md")) {
				t.Error("sync wrote into the Profile although AGENTS.md is broken")
			}
		})
	}
}

func TestSync_UntaggedInstructionsAreCopiedUnchangedToEveryProfile(t *testing.T) {
	env := newEnv(t)
	agents := "# Rules\r\n\r\nBe kind.\r\n\r\n\r\n- <!-- baton:tags is only a marker as a whole line -->\r\n"
	env.SourceFile("AGENTS.md", agents)
	env.CommitSource("add instructions")
	personal := env.MakeProfileDir(".claude-personal")
	work := env.MakeProfileDir(".claude-work")
	env.WriteConfig(twoProfilesConfig(env, "", `["work"]`))

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	want := block("# Rules\r\n\r\nBe kind.\r\n\r\n\r\n- <!-- baton:tags is only a marker as a whole line -->\n")
	for _, profile := range []string{personal, work} {
		if got := env.ReadFile(filepath.Join(profile, "CLAUDE.md")); got != want {
			t.Errorf("%s/CLAUDE.md = %q, want %q", profile, got, want)
		}
	}
}

func TestSync_TheQuestionForAnExistingInstructionsFileShowsOnlyWhatThatProfileReceives(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("AGENTS.md", taggedAgents)
	env.CommitSource("add instructions")
	env.MakeProfileDir(".claude-personal")
	work := env.MakeProfileDir(".claude-work")
	claudeMd := filepath.Join(work, "CLAUDE.md")
	env.WriteFile(claudeMd, "My rules.\n")
	env.WriteConfig(twoProfilesConfig(env, "", `["work"]`))

	res := env.RunTerminal([]string{"y"}, "sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, transcript:\n%s", res.ExitCode, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "+Use the ticket tracker.") {
		t.Errorf("transcript lacks the work part:\n%s", res.Stdout)
	}
	for _, unwanted := range []string{"Keep it short.", "baton:tags"} {
		if strings.Contains(res.Stdout, unwanted) {
			t.Errorf("transcript contains %q, which this Profile does not receive:\n%s", unwanted, res.Stdout)
		}
	}
	if got := env.ReadFile(filepath.Join(onlyBackup(t, env, "claude-work"), "CLAUDE.md")); got != "My rules.\n" {
		t.Errorf("backed up CLAUDE.md = %q, want the original", got)
	}
}
