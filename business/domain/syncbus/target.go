package syncbus

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/runtime"
)

// ProfileFiles is the file access to one Profile. It is what a Runtime
// provides to write into its Profiles: the host reads and writes plain files,
// a Compose Runtime will go through Docker (ticket 16). Everything sync does to
// a Profile goes through it, so the planning code never touches the file
// system of the host directly.
//
// rel is a slash-separated path relative to the Profile directory. A few
// Profiles keep content next to the directory, not in it (Claude's
// .claude.json, Codex's skills), and name it with a leading "../".
type ProfileFiles interface {
	// Check fails unless the Profile can be written to.
	Check(ctx context.Context) error
	// Path is rel as shown to the user and as handed to QueryTracked.
	Path(rel string) string
	// QueryEntry reads what exists at rel. It does not follow a symbolic link
	// at rel.
	QueryEntry(ctx context.Context, rel string) (Entry, error)
	// QueryShared reads the file at rel, following a symbolic link at rel.
	QueryShared(ctx context.Context, rel string) (SharedFile, error)
	// WriteFile writes the file at rel atomically.
	WriteFile(ctx context.Context, rel string, data []byte, mode fs.FileMode) error
	// WriteShared writes the file a QueryShared found, by its Path.
	WriteShared(ctx context.Context, path string, data []byte, mode fs.FileMode) error
	// RemoveFile removes the file at rel, then the directories it leaves empty
	// below the top directory of the content (skills, config.d, ...), never
	// that directory itself. A file that is already gone is fine.
	RemoveFile(ctx context.Context, rel string) error
	// RemoveShared removes the file a QueryShared found, by its Path.
	RemoveShared(ctx context.Context, path string) error
	// RemoveAll removes whatever is at rel: a directory with everything in it,
	// or a symbolic link itself, never what it points to.
	RemoveAll(ctx context.Context, rel string) error
	// QueryTracked returns those of the paths (from Path or SharedFile.Path)
	// that git tracks.
	QueryTracked(ctx context.Context, paths []string) ([]string, error)
}

// Target is a Profile that sync renders into, wherever it lives.
type Target struct {
	// Profile names the Profile, its Agent and the tags it accepts. Its Dir is
	// the directory on the host that Files works on; for a Profile in a
	// container it is informational.
	Profile configbus.Profile
	// Runtime is where the Agent of this Profile runs.
	Runtime runtime.Runtime
	// Home is the HOME the Agent sees, as a host path: the Agent's own files
	// that sit outside the Profile directory (.claude.json, .agents/skills)
	// are relative to it.
	Home string
	// HookBinary is the Baton executable as the Agent's hooks must call it:
	// the path on the host, or the path where it is mounted in a container.
	HookBinary string
	Files      ProfileFiles
	// Isolated says that a failure in this Profile (a container that cannot be
	// reached, a write that fails) is reported without stopping sync for the
	// other Profiles. The host's own Profiles are not: a mistake in the
	// config stops the whole run.
	Isolated bool
}

// CodexSkillsRel is where the user skills of Codex are, in the manifest and
// in the paths a Profile's files are asked for: $HOME/.agents/skills. In a
// sandbox it sits next to the Profile directory ($HOME/.codex), which is what
// the path says; a Profile whose directory is somewhere else (CODEX_HOME in a
// container) maps the prefix to its HOME.
const CodexSkillsRel = "../.agents/skills"

// ContentTop is the directory of rel that is never removed with the files
// below it: the first path element ("skills", "config.d"), or
// ../.agents/skills for the skills of Codex, which sit next to the Profile
// directory.
func ContentTop(rel string) string {
	if rest, ok := strings.CutPrefix(rel, CodexSkillsRel+"/"); ok && rest != "" {
		return CodexSkillsRel
	}
	return strings.Split(rel, "/")[0]
}

// layout is where an Agent keeps what sync writes, and in which form.
type layout struct {
	// skillsRel is the directory of user skills, relative to the Profile
	// directory.
	skillsRel string
	// instructionsRel is the Profile's instructions file (CLAUDE.md,
	// AGENTS.md), for the layouts that keep a marked block in it.
	instructionsRel string
	// stripSkillTags removes Baton's `tags` key from the front matter of the
	// copies of SKILL.md.
	stripSkillTags bool
	instructions   instructionsForm
	mcp            mcpForm
	// hooksFile is the shared JSON file the status hooks go into.
	hooksFile string
}

// instructionsForm says how the instructions reach the Profile.
type instructionsForm int

const (
	// blockForm is a marked block in the Profile's own instructions
	// file (CLAUDE.md), which belongs to the user.
	blockForm instructionsForm = iota
	// fragmentForm is a file of Baton's in AGENTS.d, which is merged
	// into an instructions file that something else regenerates.
	fragmentForm
)

// mcpForm says how the MCP servers reach the Profile.
type mcpForm int

const (
	// keysForm are named keys in the Agent's shared JSON configuration.
	keysForm mcpForm = iota
	// fragmentMCP is a TOML file of Baton's in config.d, which is merged into
	// a config that something else regenerates.
	fragmentMCP
	// tablesForm are named tables in the Agent's shared TOML configuration
	// (Codex's config.toml), edited without touching the rest.
	tablesForm
)

// Locations of Baton's fragments inside a Codex Profile in a sandbox.
const (
	instructionsFragmentFile = "AGENTS.d/baton.md"
	mcpFragmentFile          = "config.d/baton.toml"
)

// layoutOf returns the layout of a Target's Agent in its Runtime.
func layoutOf(t Target) (layout, error) {
	switch {
	case t.Profile.Agent == agent.Claude:
		return layout{skillsRel: "skills", instructionsRel: instructionsFile, instructions: blockForm, mcp: keysForm, hooksFile: settingsFile}, nil
	case t.Profile.Agent == agent.Codex && t.Runtime == runtime.Sandbox:
		// ai-sandbox regenerates config.toml and AGENTS.md on every run, so
		// Baton writes fragments for them. The skills directory and hooks.json
		// are not regenerated (checked against run.sh, see DESIGN.md).
		return layout{skillsRel: CodexSkillsRel, stripSkillTags: true, instructions: fragmentForm, mcp: fragmentMCP, hooksFile: codexHooksFile}, nil
	case t.Profile.Agent == agent.Codex && t.Runtime == runtime.Compose:
		// Nothing regenerates the files of a Profile in a Compose container,
		// so Codex's own files are edited in place: a marked block in
		// AGENTS.md, tables in config.toml, hooks in hooks.json.
		return layout{skillsRel: CodexSkillsRel, instructionsRel: codexInstructionsFile, stripSkillTags: true, instructions: blockForm, mcp: tablesForm, hooksFile: codexHooksFile}, nil
	default:
		return layout{}, fmt.Errorf("%s Profiles in the %s Runtime are not supported", t.Profile.Agent, t.Runtime)
	}
}
