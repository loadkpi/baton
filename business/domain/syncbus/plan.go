package syncbus

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/foundation/textdiff"
)

// profilePlan is what sync is about to do to one Profile.
type profilePlan struct {
	profile configbus.Profile
	// manifest is what the Profile's manifest says once sync is done.
	manifest Manifest
	// writes are the files whose content or mode differ from the source.
	writes    []plannedWrite
	unchanged int
	// stale are owned files that the source no longer has.
	stale []string
	// blocksBefore are the blocks the manifest records while the files are
	// being changed: the new ones, and those about to be taken out.
	blocksBefore []OwnedBlock
	// shared are the changes to files that hold a part of Baton.
	shared []sharedChange
	// asks are the things sync does not own and wants to change.
	asks []ask
}

// sharedChange is the change to the Baton block of one shared file.
type sharedChange struct {
	rel string
	// path is the file itself, with symbolic links resolved.
	path string
	data []byte
	mode fs.FileMode
	// remove deletes the file, which only held Baton's block.
	remove bool
}

type plannedWrite struct {
	rel  string
	file File
}

// ask is a thing in a Profile that sync does not own and would change. When
// the user agrees, it is backed up first; a directory or symbolic link is
// removed before the skill is written in its place.
type ask struct {
	// rel is the path inside the Profile, which names the backup; path is
	// the absolute path to copy.
	rel         string
	path        string
	question    Question
	removeFirst bool
}

// abs is the absolute path of rel inside the Profile.
func (p profilePlan) abs(rel string) string {
	return filepath.Join(p.profile.Dir, filepath.FromSlash(rel))
}

// plan decides what to do to one Profile. It reads, and writes nothing.
func (b *Bus) plan(ctx context.Context, p configbus.Profile, source Source) (profilePlan, error) {
	previous, err := b.storer.QueryManifest(ctx, p.Name)
	if err != nil {
		return profilePlan{}, err
	}
	owned := make(map[string]bool, len(previous.Files))
	for _, f := range previous.Files {
		owned[f.Path] = true
	}

	plan := profilePlan{profile: p}
	wanted := make(map[string]bool)
	for _, s := range source.Skills {
		if err := b.planSkill(ctx, &plan, owned, s); err != nil {
			return profilePlan{}, err
		}
		for _, f := range s.Files {
			wanted[path.Join("skills", s.Name, f.Path)] = true
		}
	}

	for rel := range wanted {
		plan.manifest.Files = append(plan.manifest.Files, OwnedFile{Path: rel})
	}
	sort.Slice(plan.manifest.Files, func(i, j int) bool { return plan.manifest.Files[i].Path < plan.manifest.Files[j].Path })
	for _, f := range previous.Files {
		if !wanted[f.Path] {
			plan.stale = append(plan.stale, f.Path)
		}
	}

	if err := b.planInstructions(ctx, &plan, previous, source); err != nil {
		return profilePlan{}, err
	}
	return plan, nil
}

// planSkill plans the files of one source skill.
func (b *Bus) planSkill(ctx context.Context, plan *profilePlan, owned map[string]bool, s Skill) error {
	dirRel := path.Join("skills", s.Name)

	// Nothing of this skill is ours: whatever sits at its path belongs to
	// someone else, and is replaced as a whole.
	if !ownsAnyUnder(owned, dirRel) {
		entry, err := b.storer.QueryEntry(ctx, plan.profile.Dir, dirRel)
		if err != nil {
			return err
		}
		if entry.Kind != Missing {
			plan.asks = append(plan.asks, ask{
				rel:         dirRel,
				path:        plan.abs(dirRel),
				removeFirst: true,
				question: Question{
					Profile: plan.profile.Name.String(),
					Path:    plan.abs(dirRel),
					Diff:    diffReplacement(entry, s),
				},
			})
			for _, f := range s.Files {
				plan.writes = append(plan.writes, plannedWrite{rel: path.Join(dirRel, f.Path), file: f})
			}
			return nil
		}
	}

	for _, f := range s.Files {
		rel := path.Join(dirRel, f.Path)
		entry, err := b.storer.QueryEntry(ctx, plan.profile.Dir, rel)
		if err != nil {
			return err
		}
		switch entry.Kind {
		case Missing:
			plan.writes = append(plan.writes, plannedWrite{rel: rel, file: f})
		case RegularFile:
			current := entry.Files[0]
			switch {
			case owned[rel] && sameFile(current, f):
				plan.unchanged++
			case owned[rel]:
				plan.writes = append(plan.writes, plannedWrite{rel: rel, file: f})
			default:
				plan.asks = append(plan.asks, ask{
					rel:  rel,
					path: plan.abs(rel),
					question: Question{
						Profile: plan.profile.Name.String(),
						Path:    plan.abs(rel),
						Diff:    diffFile("a/"+rel, "b/"+rel, current, f),
					},
				})
				plan.writes = append(plan.writes, plannedWrite{rel: rel, file: f})
			}
		default:
			return fmt.Errorf("%s exists and is not a regular file", plan.abs(rel))
		}
	}
	return nil
}

func ownsAnyUnder(owned map[string]bool, dirRel string) bool {
	for rel := range owned {
		if strings.HasPrefix(rel, dirRel+"/") {
			return true
		}
	}
	return false
}

func sameFile(current, want File) bool {
	return slices.Equal(current.Data, want.Data) && current.Mode.Perm() == want.Mode.Perm()
}

// diffFile is the unified diff from a file that exists to the wanted one.
func diffFile(oldName, newName string, current, want File) string {
	if diff := textdiff.Unified(oldName, newName, string(current.Data), string(want.Data)); diff != "" {
		return diff
	}
	return fmt.Sprintf("%s: mode %04o becomes %04o\n", newName, current.Mode.Perm(), want.Mode.Perm())
}

// diffReplacement describes what happens when whatever is at a skill's path is
// replaced by the skill from the source.
func diffReplacement(entry Entry, s Skill) string {
	var b strings.Builder
	prefix := "skills/" + s.Name

	switch entry.Kind {
	case Symlink:
		fmt.Fprintf(&b, "%s is a symbolic link to %s; the link is replaced by a directory, its target is not touched\n", prefix, entry.Target)
	case Directory:
	default:
		fmt.Fprintf(&b, "%s is not a directory; it is replaced by one\n", prefix)
	}

	// For a file at the skill's path the old content is the file itself.
	old := make(map[string]File)
	for _, f := range entry.Files {
		name := f.Path
		if entry.Kind == RegularFile {
			name = "(the file itself)"
		}
		old[name] = f
	}
	wanted := make(map[string]File, len(s.Files))
	for _, f := range s.Files {
		wanted[f.Path] = f
	}

	names := make([]string, 0, len(old)+len(wanted))
	for name := range old {
		names = append(names, name)
	}
	for name := range wanted {
		if _, ok := old[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	for _, name := range names {
		cur, hadOld := old[name]
		want, hasNew := wanted[name]
		oldName, newName := "a/"+prefix+"/"+name, "b/"+prefix+"/"+name
		switch {
		case hadOld && hasNew:
			b.WriteString(diffFile(oldName, newName, describe(cur), want))
		case hadOld:
			b.WriteString(textdiff.Unified(oldName, "/dev/null", string(describe(cur).Data), ""))
		default:
			b.WriteString(textdiff.Unified("/dev/null", newName, "", string(want.Data)))
		}
	}
	return b.String()
}

// describe turns a symbolic link inside a directory into text, so that it can
// be shown in a diff.
func describe(f File) File {
	if f.Mode&fs.ModeSymlink != 0 {
		return File{Path: f.Path, Mode: 0o777, Data: []byte("symbolic link to " + string(f.Data) + "\n")}
	}
	return f
}

// checkTracked stops when sync would change a file that git tracks.
func (b *Bus) checkTracked(ctx context.Context, plans []profilePlan) error {
	var paths []string
	for _, plan := range plans {
		for _, a := range plan.asks {
			paths = append(paths, plan.abs(a.rel))
		}
		for _, w := range plan.writes {
			paths = append(paths, plan.abs(w.rel))
		}
		for _, c := range plan.shared {
			paths = append(paths, c.path)
		}
		for _, rel := range plan.stale {
			paths = append(paths, plan.abs(rel))
		}
	}
	tracked, err := b.storer.QueryTracked(ctx, paths)
	if err != nil {
		return err
	}
	if len(tracked) > 0 {
		return fmt.Errorf("refusing to change %s: git tracks it", strings.Join(slices.Compact(tracked), ", "))
	}
	return nil
}

// askAll asks every question and stops at the first answer that is not yes,
// or when there is no terminal to ask in.
func (b *Bus) askAll(ctx context.Context, plans []profilePlan) error {
	for _, plan := range plans {
		for _, a := range plan.asks {
			ok, err := b.confirmer.Confirm(ctx, a.question)
			switch {
			case errors.Is(err, ErrNoTerminal):
				return fmt.Errorf("%s is not owned by Baton and there is no terminal to ask whether to change it; nothing was written", a.question.Path)
			case err != nil:
				return err
			case !ok:
				return fmt.Errorf("%s is not owned by Baton and you did not agree to change it; nothing was written", a.question.Path)
			}
		}
	}
	return nil
}

// apply carries out one Profile's plan: backups, then the manifest, then the
// removals and the writes, then the final manifest.
func (b *Bus) apply(ctx context.Context, plan profilePlan, stamp string) (ProfileResult, error) {
	pr := ProfileResult{Name: plan.profile.Name, Unchanged: plan.unchanged}

	for _, a := range plan.asks {
		copyPath, err := b.storer.SaveBackup(ctx, plan.profile.Name, stamp, a.path, a.rel)
		if err != nil {
			return ProfileResult{}, fmt.Errorf("back up %s: %w", a.question.Path, err)
		}
		pr.Backups = append(pr.Backups, Backup{Path: a.question.Path, Copy: copyPath})
	}

	// The manifest goes first: if sync dies half-way, every file it may have
	// written, and every file it was about to remove, is still recorded.
	before := plan.manifest
	for _, rel := range plan.stale {
		before.Files = append(before.Files, OwnedFile{Path: rel})
	}
	before.Blocks = plan.blocksBefore
	if err := b.storer.SaveManifest(ctx, plan.profile.Name, before); err != nil {
		return ProfileResult{}, err
	}

	for _, a := range plan.asks {
		if a.removeFirst {
			if err := b.storer.RemoveAll(ctx, plan.profile.Dir, a.rel); err != nil {
				return ProfileResult{}, err
			}
		}
	}
	for _, rel := range plan.stale {
		if err := b.storer.RemoveFile(ctx, plan.profile.Dir, rel); err != nil {
			return ProfileResult{}, err
		}
		pr.Removed++
	}
	for _, w := range plan.writes {
		if err := b.storer.WriteFile(ctx, plan.profile.Dir, w.rel, w.file.Data, w.file.Mode); err != nil {
			return ProfileResult{}, err
		}
		pr.Written++
	}

	for _, c := range plan.shared {
		if c.remove {
			err := b.storer.RemoveFile(ctx, filepath.Dir(c.path), filepath.Base(c.path))
			if err != nil {
				return ProfileResult{}, err
			}
			pr.Removed++
			continue
		}
		if err := b.storer.WriteFile(ctx, filepath.Dir(c.path), filepath.Base(c.path), c.data, c.mode); err != nil {
			return ProfileResult{}, err
		}
		pr.Written++
	}

	if err := b.storer.SaveManifest(ctx, plan.profile.Name, plan.manifest); err != nil {
		return ProfileResult{}, err
	}
	return pr, nil
}
