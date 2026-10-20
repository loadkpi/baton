// Package syncbus renders the user's source repository into Profiles.
package syncbus

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/types/profilename"
)

// Skill is a directory of the source with a SKILL.md and optional supporting
// files.
type Skill struct {
	Name  string
	Files []File
}

// File is a file inside a skill. Path is slash-separated and relative to the
// skill directory.
type File struct {
	Path string
	Mode fs.FileMode
	Data []byte
}

// SourceSkills is what the source repository holds.
type SourceSkills struct {
	Skills []Skill
	// Skipped are names of directories under skills/ that are not skills
	// because they have no SKILL.md.
	Skipped []string
}

// Manifest lists what sync owns in one Profile.
type Manifest struct {
	// Files are the files sync wrote, as slash-separated paths relative to
	// the Profile directory, sorted.
	Files []OwnedFile
}

// OwnedFile is a file in a Profile that sync created.
type OwnedFile struct {
	Path string
}

// Storer is the file storage sync works with: the source repository, the
// Profile directories and the manifests.
type Storer interface {
	// QueryManifest returns the manifest of a Profile; a Profile that was
	// never synced has an empty one.
	QueryManifest(ctx context.Context, profile profilename.Name) (Manifest, error)
	// FileExists reports whether rel (slash-separated) exists inside a
	// Profile directory, as a file, directory or anything else.
	FileExists(ctx context.Context, profileDir, rel string) (bool, error)
	// CheckProfileDir fails unless dir is an existing directory.
	CheckProfileDir(ctx context.Context, dir string) error
	// QuerySkills reads the skills of the source repository. A source without
	// a skills directory has no skills; it is an error if source itself is
	// not a directory.
	QuerySkills(ctx context.Context, source string) (SourceSkills, error)
	// SaveManifest replaces the manifest of a Profile unless it is identical.
	SaveManifest(ctx context.Context, profile profilename.Name, m Manifest) error
	// WriteFile writes a file inside a Profile directory (rel is
	// slash-separated) unless it already has exactly this content and mode.
	// It reports whether it wrote.
	WriteFile(ctx context.Context, profileDir, rel string, data []byte, mode fs.FileMode) (bool, error)
}

// Bus is the sync domain.
type Bus struct {
	storer Storer
}

// New creates the sync domain over a storage.
func New(storer Storer) *Bus {
	return &Bus{storer: storer}
}

// ProfileResult is what sync did to one Profile.
type ProfileResult struct {
	Name      profilename.Name
	Written   int
	Unchanged int
}

// Result is what one sync run did.
type Result struct {
	Profiles []ProfileResult
	// Warnings are things the user should know that did not stop the sync.
	Warnings []string
}

// profilePlan is what sync is about to do to one Profile.
type profilePlan struct {
	profile  configbus.Profile
	manifest Manifest
	writes   []plannedWrite
}

type plannedWrite struct {
	rel  string
	file File
}

// Sync renders the source repository named in cfg into every Profile of cfg.
// It first checks everything and plans all Profiles; it writes only when no
// Profile has a problem.
func (b *Bus) Sync(ctx context.Context, cfg configbus.Config) (Result, error) {
	for _, p := range cfg.Profiles {
		if err := b.storer.CheckProfileDir(ctx, p.Dir); err != nil {
			return Result{}, fmt.Errorf("profile %s: %w", p.Name, err)
		}
	}

	source, err := b.storer.QuerySkills(ctx, cfg.Source)
	if err != nil {
		return Result{}, fmt.Errorf("read source: %w", err)
	}

	var res Result
	for _, name := range source.Skipped {
		res.Warnings = append(res.Warnings, fmt.Sprintf("skipped skills/%s: it has no SKILL.md", name))
	}

	plans := make([]profilePlan, 0, len(cfg.Profiles))
	var conflicts []string
	for _, p := range cfg.Profiles {
		plan, profileConflicts, err := b.plan(ctx, p, source.Skills)
		if err != nil {
			return Result{}, fmt.Errorf("profile %s: %w", p.Name, err)
		}
		conflicts = append(conflicts, profileConflicts...)
		plans = append(plans, plan)
	}
	if len(conflicts) > 0 {
		return Result{}, fmt.Errorf("refusing to overwrite files that Baton did not create: %s", strings.Join(conflicts, ", "))
	}

	for _, plan := range plans {
		pr, err := b.apply(ctx, plan)
		if err != nil {
			return Result{}, fmt.Errorf("profile %s: %w", plan.profile.Name, err)
		}
		res.Profiles = append(res.Profiles, pr)
	}
	return res, nil
}

// plan decides what to write into one Profile and which existing files are in
// the way: files that exist but are not in the Profile's manifest.
func (b *Bus) plan(ctx context.Context, p configbus.Profile, skills []Skill) (profilePlan, []string, error) {
	previous, err := b.storer.QueryManifest(ctx, p.Name)
	if err != nil {
		return profilePlan{}, nil, err
	}
	owned := make(map[string]bool, len(previous.Files))
	for _, f := range previous.Files {
		owned[f.Path] = true
	}

	plan := profilePlan{profile: p}
	var conflicts []string
	for _, s := range skills {
		for _, f := range s.Files {
			rel := path.Join("skills", s.Name, f.Path)
			plan.writes = append(plan.writes, plannedWrite{rel: rel, file: f})
			if owned[rel] {
				continue
			}
			exists, err := b.storer.FileExists(ctx, p.Dir, rel)
			if err != nil {
				return profilePlan{}, nil, err
			}
			if exists {
				conflicts = append(conflicts, filepath.Join(p.Dir, filepath.FromSlash(rel)))
			}
			owned[rel] = true
		}
	}

	// Ownership is never forgotten here: files recorded earlier stay in the
	// manifest even when the source no longer has them.
	for rel := range owned {
		plan.manifest.Files = append(plan.manifest.Files, OwnedFile{Path: rel})
	}
	sort.Slice(plan.manifest.Files, func(i, j int) bool { return plan.manifest.Files[i].Path < plan.manifest.Files[j].Path })
	return plan, conflicts, nil
}

// apply writes the manifest, then the files.
func (b *Bus) apply(ctx context.Context, plan profilePlan) (ProfileResult, error) {
	pr := ProfileResult{Name: plan.profile.Name}

	// The manifest goes first: if sync dies half-way, every file it may have
	// written is already recorded as owned.
	if err := b.storer.SaveManifest(ctx, plan.profile.Name, plan.manifest); err != nil {
		return ProfileResult{}, err
	}

	for _, w := range plan.writes {
		wrote, err := b.storer.WriteFile(ctx, plan.profile.Dir, w.rel, w.file.Data, w.file.Mode)
		if err != nil {
			return ProfileResult{}, err
		}
		if wrote {
			pr.Written++
		} else {
			pr.Unchanged++
		}
	}
	return pr, nil
}
