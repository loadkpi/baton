// Package syncbus renders the user's source repository into Profiles.
//
// Sync owns only what it created. It records every file in a manifest and
// updates or deletes only those. Where it would have to change something it
// does not own, it shows the difference, asks, and backs the thing up first.
// It never writes a file that git tracks.
package syncbus

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/tag"
)

// Skill is a directory of the source with a SKILL.md and optional supporting
// files.
type Skill struct {
	Name string
	// Tags are the tags in the front matter of SKILL.md; none means the
	// skill goes to every Profile.
	Tags  []tag.Tag
	Files []File
}

// File is a file inside a skill. Path is slash-separated and relative to the
// skill directory.
type File struct {
	Path string
	Mode fs.FileMode
	Data []byte
}

// Source is what the source repository holds: the instructions, the skills and
// the MCP servers.
type Source struct {
	// Instructions is the content of the source's AGENTS.md; HasInstructions
	// is false when there is none (or it is empty).
	Instructions    string
	HasInstructions bool
	Skills          []Skill
	// MCP are the servers of the source's MCP manifest.
	MCP []MCPServer
	// Skipped are names of directories under skills/ that are not skills
	// because they have no SKILL.md.
	Skipped []string
}

// Manifest lists what sync owns in one Profile.
type Manifest struct {
	// Files are the files sync wrote, as slash-separated paths relative to
	// the Profile directory, sorted.
	Files []OwnedFile
	// Blocks are the blocks of shared Markdown files that sync owns.
	Blocks []OwnedBlock
	// Keys are the named keys of shared JSON files that sync owns.
	Keys []OwnedKey
}

// OwnedKey is a key of a shared JSON file (for example an entry of mcpServers
// in .claude.json) that sync owns, and nothing else in the file.
type OwnedKey struct {
	// Path is relative to the Profile directory, slash-separated.
	Path string
	// Key leads from the top of the document to the key: ["mcpServers", "x"].
	Key []string
	// Item is set when Key leads to an array that is shared with the user and
	// sync owns one element of it: the hook handler group whose command is
	// Item (a Claude hook in settings.json).
	Item string
	// Sum is the SHA-256 (hex) of the canonical JSON of the value sync last
	// wrote. A different value in the file means someone edited it by hand.
	Sum string
}

// OwnedFile is a file in a Profile that sync created.
type OwnedFile struct {
	Path string
}

// OwnedBlock is the block of Baton in a shared Markdown file (for example
// CLAUDE.md): the part between the markers, and nothing else in the file.
type OwnedBlock struct {
	// Path is relative to the Profile directory, slash-separated.
	Path string
	// Sum is the SHA-256 (hex) of the body sync last wrote. A different
	// body in the file means someone edited the block by hand.
	Sum string
}

// SharedFile is a file that belongs to someone else and holds a part of
// Baton. If the path is a symbolic link, it is resolved, so the file behind it
// is the one that is read and changed.
type SharedFile struct {
	Exists bool
	// Path is the absolute path of the file itself, links resolved.
	Path   string
	Linked bool
	Mode   fs.FileMode
	Data   []byte
}

// EntryKind says what exists at a path of a Profile.
type EntryKind int

// The kinds of Entry.
const (
	// Missing means nothing is there.
	Missing EntryKind = iota
	// RegularFile is a file; Entry.Files holds it, with an empty Path.
	RegularFile
	// Directory is a directory; Entry.Files holds every file below it.
	Directory
	// Symlink is a symbolic link; Entry.Target is where it points.
	Symlink
	// Other is anything else (a socket, a device).
	Other
)

// Entry is what exists at a path of a Profile, with its content.
type Entry struct {
	Kind EntryKind
	// Files are the files of a RegularFile (one, with an empty Path) or a
	// Directory (all below it, sorted, relative to it). A symbolic link
	// inside a directory is a File with fs.ModeSymlink in its Mode and its
	// target as Data.
	Files  []File
	Target string
}

// Storer is the storage sync works with outside the Profiles: the source
// repository, the secrets, the manifests and the backups. The files of a
// Profile are reached through its Target (ProfileFiles).
type Storer interface {
	// QueryManifest returns the manifest of a Profile; a Profile that was
	// never synced has an empty one.
	QueryManifest(ctx context.Context, profile profilename.Name) (Manifest, error)
	// SaveManifest replaces the manifest of a Profile unless it is identical.
	SaveManifest(ctx context.Context, profile profilename.Name, m Manifest) error
	// QuerySource reads the instructions and the skills of the source
	// repository. A source without a skills directory has no skills; it is an
	// error if source itself is not a directory.
	QuerySource(ctx context.Context, source string) (Source, error)
	// QuerySecrets reads the values of ${VAR} placeholders. A missing secrets
	// file holds no values; one that others can read is an error.
	QuerySecrets(ctx context.Context) (Secrets, error)
	// SaveBackup writes a copy of what a Profile had at rel to the backups of
	// the Profile, inside the directory named stamp. It returns where the copy
	// is and never overwrites one.
	SaveBackup(ctx context.Context, profile profilename.Name, stamp, rel string, entry Entry) (string, error)
	// QueryExecutable reports whether path is a file somebody can execute.
	QueryExecutable(ctx context.Context, path string) (bool, error)
}

// Question is what sync asks the user before it changes something it does not
// own.
type Question struct {
	Profile string
	// Path is the absolute path that would change.
	Path string
	// Diff shows what would change, in unified diff format.
	Diff string
}

// ErrNoTerminal is returned by a Confirmer that has no terminal to ask in.
var ErrNoTerminal = errors.New("no terminal to ask in")

// Confirmer asks the user a Question.
type Confirmer interface {
	// Confirm reports whether the user agreed to the change. It returns
	// ErrNoTerminal when it cannot ask.
	Confirm(ctx context.Context, q Question) (bool, error)
}

// Bus is the sync domain.
type Bus struct {
	storer    Storer
	confirmer Confirmer
	now       func() time.Time
}

// New creates the sync domain over a storage and a way to ask the user.
func New(storer Storer, confirmer Confirmer) *Bus {
	return &Bus{storer: storer, confirmer: confirmer, now: time.Now}
}

// Backup is a copy sync made of something before it changed it.
type Backup struct {
	// Path is what was copied; Copy is where the copy is.
	Path string
	Copy string
}

// ProfileResult is what sync did to one Profile.
type ProfileResult struct {
	Name      profilename.Name
	Agent     agent.Agent
	Written   int
	Removed   int
	Unchanged int
	Backups   []Backup
	// HooksFirst is set when this run gave the Profile Baton's status hooks
	// for the first time. Sessions already running keep their old hooks.
	HooksFirst bool
}

// Result is what one sync run did.
type Result struct {
	Profiles []ProfileResult
	// Warnings are things the user should know that did not stop the sync.
	Warnings []string
}

// Sync renders the source repository named in cfg into every target. It first
// checks everything, plans all Profiles, refuses to touch tracked files and
// asks every question; it writes only when no Profile has a problem and every
// question was answered yes.
func (b *Bus) Sync(ctx context.Context, cfg configbus.Config, targets []Target) (Result, error) {
	seen := make(map[string]bool, len(targets))
	for _, t := range targets {
		name := t.Profile.Name.String()
		if seen[name] {
			return Result{}, fmt.Errorf("two Profiles are called %s; rename the one in the config", name)
		}
		seen[name] = true
		if err := t.Files.Check(ctx); err != nil {
			return Result{}, fmt.Errorf("profile %s: %w", t.Profile.Name, err)
		}
	}

	source, err := b.storer.QuerySource(ctx, cfg.Source)
	if err != nil {
		return Result{}, fmt.Errorf("read source: %w", err)
	}

	secrets, err := b.storer.QuerySecrets(ctx)
	if err != nil {
		return Result{}, err
	}

	var res Result
	for _, name := range source.Skipped {
		res.Warnings = append(res.Warnings, fmt.Sprintf("skipped skills/%s: it has no SKILL.md", name))
	}

	plans := make([]profilePlan, 0, len(targets))
	for _, t := range targets {
		plan, err := b.plan(ctx, t, source, secrets, &res)
		if err != nil {
			return Result{}, fmt.Errorf("profile %s: %w", t.Profile.Name, err)
		}
		plans = append(plans, plan)
	}

	if err := b.warnAboutHookBinary(ctx, plans, cfg.Home, &res); err != nil {
		return Result{}, err
	}

	if err := b.checkTracked(ctx, plans); err != nil {
		return Result{}, err
	}
	if err := b.askAll(ctx, plans); err != nil {
		return Result{}, err
	}

	stamp := b.now().UTC().Format("20060102T150405.000Z")
	for _, plan := range plans {
		pr, err := b.apply(ctx, plan, stamp)
		if err != nil {
			return Result{}, fmt.Errorf("profile %s: %w", plan.profile.Name, err)
		}
		pr.Agent = plan.profile.Agent
		pr.HooksFirst = plan.hooksFirst
		res.Profiles = append(res.Profiles, pr)
	}
	return res, nil
}

// warnAboutHookBinary warns when the hooks that are installed call a binary
// that is not there: every hook fails until it is. Sync still succeeds.
func (b *Bus) warnAboutHookBinary(ctx context.Context, plans []profilePlan, home string, res *Result) error {
	wanted := false
	for _, plan := range plans {
		wanted = wanted || plan.hooks
	}
	if !wanted {
		return nil
	}
	ok, err := b.storer.QueryExecutable(ctx, hostHookBinary(home))
	if err != nil || ok {
		return err
	}
	res.Warnings = append(res.Warnings, fmt.Sprintf("the status hooks call %s, which is missing or not executable, so they fail until it is there; run `make install` in the Baton repository or copy the binary there", hostHookBinary(home)))
	return nil
}
