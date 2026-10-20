package test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSync_InstallsTheBuiltInHandoffSkillIntoEveryHostProfileWhateverTheTagsAndTheSource(t *testing.T) {
	env := newEnv(t)
	// The source has no skills at all, and the Profile accepts only "work".
	env.SourceFile("AGENTS.md", "Be brief.\n")
	env.CommitSource("instructions only")
	profile := env.MakeProfileDir(".claude-personal")
	other := env.MakeProfileDir(".claude-work")
	env.WriteConfig(`source = "` + env.SourceDir + `"

[profiles.claude-personal]
agent = "claude"
dir = "` + profile + `"
tags = ["work"]

[profiles.claude-work]
agent = "claude"
dir = "` + other + `"
`)

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	for _, dir := range []string{profile, other} {
		skill := env.ReadFile(filepath.Join(dir, "skills", "baton-handoff", "SKILL.md"))
		if !strings.HasPrefix(skill, "---\nname: baton-handoff\ndescription: ") {
			t.Errorf("%s: SKILL.md starts with %q, want front matter with name baton-handoff and a description", dir, skill[:min(len(skill), 80)])
		}
	}
	skill := env.ReadFile(filepath.Join(profile, "skills", "baton-handoff", "SKILL.md"))
	for _, want := range []string{"Goal", "Done so far", "Decisions and why", "Rejected options and why", "Next step", "Open questions", "How to verify",
		"/baton/bin/baton", "~/.baton/bin/baton", "baton handoff save", "/baton-handoff"} {
		if !strings.Contains(skill, want) {
			t.Errorf("SKILL.md lacks %q", want)
		}
	}
	if got := manifestPaths(t, env, "claude-personal"); len(got) != 1 || got[0] != "skills/baton-handoff/SKILL.md" {
		t.Errorf("manifest files = %v, want the built-in skill", got)
	}
}

func TestSync_TheBuiltInSkillFollowsTheManifestLikeAnyOther(t *testing.T) {
	env := newEnv(t)
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	if res := env.Run("sync"); res.ExitCode != 0 {
		t.Fatalf("first sync: exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	path := filepath.Join(profile, "skills", "baton-handoff", "SKILL.md")
	before := env.ModTime(path)

	second := env.Run("sync")

	if second.ExitCode != 0 {
		t.Fatalf("second sync: exit code = %d, stderr=%q", second.ExitCode, second.Stderr)
	}
	if !strings.Contains(second.Stdout, "claude-personal: 0 files written, 0 removed, ") {
		t.Errorf("stdout = %q, want the second run to leave the built-in skill alone", second.Stdout)
	}
	if after := env.ModTime(path); !after.Equal(before) {
		t.Errorf("the skill was rewritten: %v -> %v", before, after)
	}
}

// A skill that is already in the Profile and is not Baton's is asked about,
// as for any skill of the source.
func TestSync_ASkillAlreadyCalledBatonHandoffInTheProfileIsAskedAboutAndBackedUp(t *testing.T) {
	env := newEnv(t)
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	path := filepath.Join(profile, "skills", "baton-handoff", "SKILL.md")
	env.WriteFile(path, "mine\n")

	refused := env.Run("sync")

	if dir := filepath.Dir(path); refused.ExitCode == 0 || !strings.Contains(refused.Stderr, dir) {
		t.Errorf("exit code = %d, stderr=%q, want a refusal that names %s (no terminal to ask in)", refused.ExitCode, refused.Stderr, dir)
	}
	if got := env.ReadFile(path); got != "mine\n" {
		t.Errorf("the file changed without an answer: %q", got)
	}
}

func TestSync_ASourceSkillNamedBatonDashAnythingIsAnErrorThatNamesItAndWritesNothing(t *testing.T) {
	env := newEnv(t)
	env.SourceFile("skills/baton-notes/SKILL.md", "---\nname: baton-notes\ndescription: Mine\n---\nNotes.\n")
	env.SourceFile("skills/greet/SKILL.md", "Hello.\n")
	env.CommitSource("add skills")
	profile := env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))

	res := env.Run("sync")

	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "skills/baton-notes") || !strings.Contains(res.Stderr, "baton-") {
		t.Errorf("exit code = %d, stderr=%q, want an error that names skills/baton-notes and the reserved prefix", res.ExitCode, res.Stderr)
	}
	if env.Exists(filepath.Join(profile, "skills")) {
		t.Error("sync wrote skills although the source was refused")
	}
}

func TestSandboxSync_BothSandboxProfilesGetTheBuiltInHandoffSkill(t *testing.T) {
	env := newEnv(t)
	env.MakeProfileDir(".claude-personal")
	env.WriteConfig(standardConfig(env))
	_, home := sandboxProject(t, env, "app")

	res := env.Run("sync")

	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	for _, rel := range []string{".claude/skills/baton-handoff/SKILL.md", ".agents/skills/baton-handoff/SKILL.md"} {
		if got := env.ReadFile(filepath.Join(home, rel)); !strings.Contains(got, "baton handoff save") {
			t.Errorf("%s = %q, want the skill that calls baton handoff save", rel, got)
		}
	}
}
