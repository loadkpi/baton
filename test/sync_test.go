package test

import (
	"encoding/json"
	"path/filepath"
	"slices"
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
	want := []string{"skills/greet/SKILL.md", "skills/greet/scripts/hello.sh"}
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
	if env.Exists(filepath.Join(profile, "skills")) {
		t.Error("sync created skills/ although the source has no skills")
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

func TestSync_RefusesToOverwriteFileItDoesNotOwn(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/greet/SKILL.md", "Hello from the source.\n")
	env.SourceFile("skills/review/SKILL.md", "Review.\n")
	env.CommitSource("add skills")
	profile := env.MakeProfileDir(".claude-personal")
	handMade := filepath.Join(profile, "skills/greet/SKILL.md")
	env.WriteFile(handMade, "Hand-made greeting.\n")
	env.WriteConfig(standardConfig(env))

	res := env.Run("sync")

	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, handMade) {
		t.Errorf("stderr = %q, want it to name %s", res.Stderr, handMade)
	}
	if got := env.ReadFile(handMade); got != "Hand-made greeting.\n" {
		t.Errorf("hand-made file = %q, want it untouched", got)
	}
	if env.Exists(filepath.Join(profile, "skills/review")) {
		t.Error("sync wrote other files although it stopped on a conflict")
	}
	if env.Exists(".baton/manifests/claude-personal.json") {
		t.Error("sync recorded a manifest although it stopped on a conflict")
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
