package syncbus

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/types/runtime"
	"github.com/loadkpi/baton/business/types/tag"
	"github.com/loadkpi/baton/foundation/frontmatter"
	"github.com/loadkpi/baton/foundation/textdiff"
)

// profilePlan is what sync is about to do to one Profile.
type profilePlan struct {
	profile configbus.Profile
	// files is how the Profile is read and written; layout is where things go.
	files  ProfileFiles
	layout layout
	// runtime and binary say how the Profile's status hooks call Baton.
	runtime runtime.Runtime
	binary  string
	// home is the HOME the Agent sees.
	home string
	// hidden are the secrets that no question may show.
	hidden map[string]string
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
	// keysBefore is the same for owned keys.
	keysBefore []OwnedKey
	// shared are the changes to files that hold a part of Baton.
	shared []sharedChange
	// asks are the things sync does not own and wants to change.
	asks []ask
	// hooks is set when the Profile gets Baton's status hooks; hooksFirst
	// when it had none before.
	hooks      bool
	hooksFirst bool
	// isolated is Target.Isolated.
	isolated bool
}

// sharedChange is the change to the Baton block of one shared file.
type sharedChange struct {
	rel string
	// path is the file itself, with symbolic links resolved (SharedFile.Path).
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
	// rel is the path inside the Profile, which names the backup; snapshot is
	// what is there now, which the backup keeps.
	rel         string
	snapshot    Entry
	question    Question
	removeFirst bool
}

// abs is the path of rel inside the Profile as it is shown to the user.
func (p profilePlan) abs(rel string) string {
	return p.files.Path(rel)
}

// snapshotOf is what a shared file holds now, for its backup.
func snapshotOf(f SharedFile) Entry {
	return Entry{Kind: RegularFile, Files: []File{{Mode: f.Mode, Data: f.Data}}}
}

// plan decides what to do to one Profile. It reads, and writes nothing.
func (b *Bus) plan(ctx context.Context, t Target, source Source, secrets Secrets, res *Result) (profilePlan, error) {
	lay, err := layoutOf(t)
	if err != nil {
		return profilePlan{}, err
	}
	previous, err := b.storer.QueryManifest(ctx, t.Profile.Name)
	if err != nil {
		return profilePlan{}, err
	}
	owned := make(map[string]bool, len(previous.Files))
	for _, f := range previous.Files {
		owned[f.Path] = true
	}

	plan := profilePlan{profile: t.Profile, files: t.Files, layout: lay, runtime: t.Runtime, binary: t.HookBinary, home: t.Home, hidden: secrets.Values}
	wanted := make(map[string]bool)
	for _, s := range source.Skills {
		if !tag.AcceptedBy(s.Tags, t.Profile.Tags) {
			continue
		}
		skill, err := lay.skillFor(s)
		if err != nil {
			return profilePlan{}, err
		}
		if err := b.planSkill(ctx, &plan, owned, skill); err != nil {
			return profilePlan{}, err
		}
		for _, f := range skill.Files {
			wanted[path.Join(lay.skillsRel, skill.Name, f.Path)] = true
		}
	}

	if err := b.planInstructions(ctx, &plan, previous, source, owned, wanted); err != nil {
		return profilePlan{}, err
	}
	if err := b.planMCP(ctx, &plan, previous, source, secrets, owned, wanted, res); err != nil {
		return profilePlan{}, err
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

	if err := b.planHooks(ctx, &plan, previous); err != nil {
		return profilePlan{}, err
	}
	return plan, nil
}

// skillFor returns the skill as it is copied into this layout's Profile.
func (l layout) skillFor(s Skill) (Skill, error) {
	if !l.stripSkillTags {
		return s, nil
	}
	files := make([]File, len(s.Files))
	copy(files, s.Files)
	for i, f := range files {
		if f.Path != "SKILL.md" {
			continue
		}
		stripped, err := frontmatter.Remove(string(f.Data), "tags")
		if err != nil {
			return Skill{}, fmt.Errorf("skills/%s/SKILL.md: %w", s.Name, err)
		}
		files[i].Data = []byte(stripped)
	}
	s.Files = files
	return s, nil
}

// planSkill plans the files of one source skill.
func (b *Bus) planSkill(ctx context.Context, plan *profilePlan, owned map[string]bool, s Skill) error {
	dirRel := path.Join(plan.layout.skillsRel, s.Name)

	// Nothing of this skill is ours: whatever sits at its path belongs to
	// someone else, and is replaced as a whole.
	if !ownsAnyUnder(owned, dirRel) {
		entry, err := plan.files.QueryEntry(ctx, dirRel)
		if err != nil {
			return err
		}
		if entry.Kind != Missing {
			plan.asks = append(plan.asks, ask{
				rel:         dirRel,
				snapshot:    entry,
				removeFirst: true,
				question: Question{
					Profile: plan.profile.Name.String(),
					Path:    plan.abs(dirRel),
					Diff:    diffReplacement(entry, dirRel, s),
				},
			})
			for _, f := range s.Files {
				plan.writes = append(plan.writes, plannedWrite{rel: path.Join(dirRel, f.Path), file: f})
			}
			return nil
		}
	}

	for _, f := range s.Files {
		if err := b.planOwnedFile(ctx, plan, owned, path.Join(dirRel, f.Path), f); err != nil {
			return err
		}
	}
	return nil
}

// planOwnedFile plans one file that sync wants to own: a file of a skill or a
// fragment. A file that is not there is written, one that sync wrote is
// updated, and one that is somebody else's is a question.
func (b *Bus) planOwnedFile(ctx context.Context, plan *profilePlan, owned map[string]bool, rel string, f File) error {
	entry, err := plan.files.QueryEntry(ctx, rel)
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
				rel:      rel,
				snapshot: entry,
				question: Question{
					Profile: plan.profile.Name.String(),
					Path:    plan.abs(rel),
					Diff:    hide(diffFile("a/"+rel, "b/"+rel, current, f), plan.hidden),
				},
			})
			plan.writes = append(plan.writes, plannedWrite{rel: rel, file: f})
		}
	default:
		return fmt.Errorf("%s exists and is not a regular file", plan.abs(rel))
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
func diffReplacement(entry Entry, prefix string, s Skill) string {
	var b strings.Builder

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
	var tracked []string
	for _, plan := range plans {
		var paths []string
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
		found, err := plan.files.QueryTracked(ctx, paths)
		if err != nil {
			return err
		}
		tracked = append(tracked, found...)
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
		copyPath, err := b.storer.SaveBackup(ctx, plan.profile.Name, stamp, a.rel, a.snapshot)
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
	before.Keys = plan.keysBefore
	if err := b.storer.SaveManifest(ctx, plan.profile.Name, before); err != nil {
		return ProfileResult{}, err
	}

	for _, a := range plan.asks {
		if a.removeFirst {
			if err := plan.files.RemoveAll(ctx, a.rel); err != nil {
				return ProfileResult{}, err
			}
		}
	}
	for _, rel := range plan.stale {
		if err := plan.files.RemoveFile(ctx, rel); err != nil {
			return ProfileResult{}, err
		}
		pr.Removed++
	}
	for _, w := range plan.writes {
		if err := plan.files.WriteFile(ctx, w.rel, w.file.Data, w.file.Mode); err != nil {
			return ProfileResult{}, err
		}
		pr.Written++
	}

	for _, c := range plan.shared {
		if c.remove {
			if err := plan.files.RemoveShared(ctx, c.path); err != nil {
				return ProfileResult{}, err
			}
			pr.Removed++
			continue
		}
		if err := plan.files.WriteShared(ctx, c.path, c.data, c.mode); err != nil {
			return ProfileResult{}, err
		}
		pr.Written++
	}

	if err := b.storer.SaveManifest(ctx, plan.profile.Name, plan.manifest); err != nil {
		return ProfileResult{}, err
	}
	return pr, nil
}
