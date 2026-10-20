// Package syncfile is the file storage of the sync domain.
package syncfile

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/loadkpi/baton/business/domain/syncbus"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/tag"
	"github.com/loadkpi/baton/foundation/atomicfile"
	"github.com/loadkpi/baton/foundation/frontmatter"
	"github.com/loadkpi/baton/foundation/git"
)

// Store reads the source repository, writes Profile directories and keeps
// the manifests in the Baton state directory.
type Store struct {
	batonDir    string
	secretsPath string
}

// New creates the sync storage. batonDir is the Baton state directory
// (~/.baton); secretsPath is the secrets file (~/.config/baton/secrets.env).
func New(batonDir, secretsPath string) *Store {
	return &Store{batonDir: batonDir, secretsPath: secretsPath}
}

// CheckProfileDir fails unless dir is an existing directory. Sync never creates
// a Profile directory: a mistyped path should be an error, not a new directory.
func (s *Store) CheckProfileDir(_ context.Context, dir string) error {
	return checkDir(dir)
}

func checkDir(dir string) error {
	info, err := os.Stat(dir)
	switch {
	case os.IsNotExist(err):
		return fmt.Errorf("directory %s does not exist", dir)
	case err != nil:
		return err
	case !info.IsDir():
		return fmt.Errorf("%s is not a directory", dir)
	}
	return nil
}

// QuerySource reads <source>/AGENTS.md and every skill directory under
// <source>/skills. Entries
// starting with "." and plain files are ignored; a directory without SKILL.md
// is reported as skipped.
func (s *Store) QuerySource(_ context.Context, source string) (syncbus.Source, error) {
	if err := checkDir(source); err != nil {
		return syncbus.Source{}, fmt.Errorf("source repository: %w", err)
	}

	var res syncbus.Source
	instructions, err := os.ReadFile(filepath.Join(source, "AGENTS.md"))
	switch {
	case err == nil:
		res.Instructions = string(instructions)
		res.HasInstructions = strings.TrimSpace(res.Instructions) != ""
	case !os.IsNotExist(err):
		return syncbus.Source{}, err
	}

	if res.MCP, err = readMCP(source); err != nil {
		return syncbus.Source{}, err
	}

	root := filepath.Join(source, "skills")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return res, nil
	}
	if err != nil {
		return syncbus.Source{}, err
	}

	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		files, err := readFiles(filepath.Join(root, e.Name()))
		if err != nil {
			return syncbus.Source{}, err
		}
		i := slices.IndexFunc(files, func(f syncbus.File) bool { return f.Path == "SKILL.md" })
		if i < 0 {
			res.Skipped = append(res.Skipped, e.Name())
			continue
		}
		skill, err := toBusSkill(e.Name(), files, files[i])
		if err != nil {
			return syncbus.Source{}, err
		}
		res.Skills = append(res.Skills, skill)
	}
	return res, nil
}

// toBusSkill builds a skill from its files. The tags are the `tags` key of the
// front matter of SKILL.md; an invalid one is an error naming the skill,
// because a skill whose tags cannot be read must not be sent anywhere.
func toBusSkill(name string, files []syncbus.File, skillMd syncbus.File) (syncbus.Skill, error) {
	list, _, err := frontmatter.List(string(skillMd.Data), "tags")
	if err != nil {
		return syncbus.Skill{}, fmt.Errorf("skills/%s/SKILL.md: tags: %w", name, err)
	}
	tags, err := tag.ParseAll(list)
	if err != nil {
		return syncbus.Skill{}, fmt.Errorf("skills/%s/SKILL.md: %w", name, err)
	}
	return syncbus.Skill{Name: name, Tags: tags, Files: files}, nil
}

// readFiles reads all regular files below dir, sorted by path.
func readFiles(dir string) ([]syncbus.File, error) {
	var files []syncbus.File
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s: only regular files and directories are supported", p)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		files = append(files, syncbus.File{
			Path: filepath.ToSlash(rel),
			Mode: normalizeMode(info.Mode()),
			Data: data,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// normalizeMode keeps only the executable bit, so Profiles do not depend on
// the umask of whoever checked the source out.
func normalizeMode(m fs.FileMode) fs.FileMode {
	if m&0o100 != 0 {
		return 0o755
	}
	return 0o644
}

// WriteFile writes rel (slash-separated) inside profileDir atomically.
func (s *Store) WriteFile(_ context.Context, profileDir, rel string, data []byte, mode fs.FileMode) error {
	return atomicfile.Write(filepath.Join(profileDir, filepath.FromSlash(rel)), data, mode)
}

// RemoveAll removes rel (slash-separated) inside profileDir: a directory with
// its content, or a symbolic link itself (never its target).
func (s *Store) RemoveAll(_ context.Context, profileDir, rel string) error {
	return os.RemoveAll(filepath.Join(profileDir, filepath.FromSlash(rel)))
}

// QueryTracked returns those of paths that git tracks.
func (s *Store) QueryTracked(ctx context.Context, paths []string) ([]string, error) {
	return git.Tracked(ctx, paths)
}

// RemoveFile removes rel (slash-separated) inside profileDir and then the
// directories that became empty, up to but excluding the first path element.
func (s *Store) RemoveFile(_ context.Context, profileDir, rel string) error {
	full := filepath.Join(profileDir, filepath.FromSlash(rel))
	if info, err := os.Lstat(full); err == nil && info.IsDir() {
		return fmt.Errorf("%s is a directory, not a file", full)
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}

	top := filepath.Join(profileDir, strings.SplitN(filepath.FromSlash(rel), string(filepath.Separator), 2)[0])
	for dir := filepath.Dir(full); dir != top && strings.HasPrefix(dir, top+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if err := os.Remove(dir); err != nil {
			break // not empty (or gone): the directory has other content
		}
	}
	return nil
}

// fileManifest is the JSON document of a manifest. Primitive types only.
//
// Entries are objects, not bare strings, so later versions can add fields
// (owned blocks and keys of shared files) without breaking this format.
type fileManifest struct {
	Version int         `json:"version"`
	Profile string      `json:"profile"`
	Files   []fileOwned `json:"files"`
	Blocks  []fileBlock `json:"blocks,omitempty"`
	Keys    []fileKey   `json:"keys,omitempty"`
}

type fileKey struct {
	Path   string   `json:"path"`
	Key    []string `json:"key"`
	Item   string   `json:"item,omitempty"`
	SHA256 string   `json:"sha256"`
}

type fileBlock struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type fileOwned struct {
	Path string `json:"path"`
}

const manifestVersion = 1

func toFileManifest(profile profilename.Name, m syncbus.Manifest) fileManifest {
	fm := fileManifest{Version: manifestVersion, Profile: profile.String(), Files: []fileOwned{}}
	for _, f := range m.Files {
		fm.Files = append(fm.Files, fileOwned{Path: f.Path})
	}
	for _, b := range m.Blocks {
		fm.Blocks = append(fm.Blocks, fileBlock{Path: b.Path, SHA256: b.Sum})
	}
	for _, k := range m.Keys {
		fm.Keys = append(fm.Keys, fileKey{Path: k.Path, Key: k.Key, Item: k.Item, SHA256: k.Sum})
	}
	return fm
}

func (s *Store) manifestPath(profile profilename.Name) string {
	return filepath.Join(s.batonDir, "manifests", profile.String()+".json")
}

// SaveManifest writes ~/.baton/manifests/<profile>.json atomically.
func (s *Store) SaveManifest(_ context.Context, profile profilename.Name, m syncbus.Manifest) error {
	data, err := json.MarshalIndent(toFileManifest(profile, m), "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.manifestPath(profile))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_, err = atomicfile.WriteIfChanged(s.manifestPath(profile), append(data, '\n'), 0o600)
	return err
}

// QueryEntry reads what is at rel (slash-separated) inside profileDir. A
// symbolic link is reported as such, not followed.
func (s *Store) QueryEntry(_ context.Context, profileDir, rel string) (syncbus.Entry, error) {
	full := filepath.Join(profileDir, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	switch {
	case os.IsNotExist(err):
		return syncbus.Entry{Kind: syncbus.Missing}, nil
	case err != nil:
		return syncbus.Entry{}, err
	}

	switch {
	case info.Mode().IsRegular():
		data, err := os.ReadFile(full)
		if err != nil {
			return syncbus.Entry{}, err
		}
		return syncbus.Entry{Kind: syncbus.RegularFile, Files: []syncbus.File{{Mode: info.Mode().Perm(), Data: data}}}, nil
	case info.IsDir():
		files, err := readTree(full)
		if err != nil {
			return syncbus.Entry{}, err
		}
		return syncbus.Entry{Kind: syncbus.Directory, Files: files}, nil
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(full)
		if err != nil {
			return syncbus.Entry{}, err
		}
		return syncbus.Entry{Kind: syncbus.Symlink, Target: target}, nil
	default:
		return syncbus.Entry{Kind: syncbus.Other}, nil
	}
}

// readTree reads everything below dir for showing and comparing: files with
// their content, symbolic links with their target. Other special files are
// left out.
func readTree(dir string) ([]syncbus.File, error) {
	var files []syncbus.File
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		f := syncbus.File{Path: filepath.ToSlash(rel)}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			f.Mode, f.Data = fs.ModeSymlink|0o777, []byte(target)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			if f.Data, err = os.ReadFile(p); err != nil {
				return err
			}
			f.Mode = info.Mode().Perm()
		default:
			return nil
		}
		files = append(files, f)
		return nil
	})
	return files, err
}

// SaveBackup copies src to <baton dir>/backups/<profile>/<stamp>/<rel>.
func (s *Store) SaveBackup(_ context.Context, profile profilename.Name, stamp, src, rel string) (string, error) {
	dst := filepath.Join(s.batonDir, "backups", profile.String(), stamp, filepath.FromSlash(rel))
	if err := copyTree(src, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// QueryShared reads rel (slash-separated) inside profileDir and follows a
// symbolic link at rel to the file behind it.
func (s *Store) QueryShared(_ context.Context, profileDir, rel string) (syncbus.SharedFile, error) {
	full := filepath.Join(profileDir, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	switch {
	case os.IsNotExist(err):
		return syncbus.SharedFile{Path: full}, nil
	case err != nil:
		return syncbus.SharedFile{}, err
	}

	file := syncbus.SharedFile{Exists: true, Path: full}
	if info.Mode()&fs.ModeSymlink != 0 {
		if file.Path, err = filepath.EvalSymlinks(full); err != nil {
			return syncbus.SharedFile{}, fmt.Errorf("%s: %w", full, err)
		}
		file.Linked = true
		if info, err = os.Stat(file.Path); err != nil {
			return syncbus.SharedFile{}, err
		}
	}
	if !info.Mode().IsRegular() {
		return syncbus.SharedFile{}, fmt.Errorf("%s is not a regular file", full)
	}
	file.Mode = info.Mode().Perm()
	if file.Data, err = os.ReadFile(file.Path); err != nil {
		return syncbus.SharedFile{}, err
	}
	return file, nil
}

// copyFile copies the content of src into a new file dst; it fails if dst
// exists.
func copyFile(src, dst string, perm fs.FileMode) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
	}()
	_, err = io.Copy(out, in)
	return err
}

// copyTree copies a file, a symbolic link (as a link) or a directory. It
// refuses to overwrite anything, so a backup never replaces an older one.
func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case info.IsDir():
		if err := os.MkdirAll(dst, 0o700); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case info.Mode().IsRegular():
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		return copyFile(src, dst, info.Mode().Perm()|0o600)
	default:
		return nil // sockets and devices have nothing to back up
	}
}

// toBusManifest converts the manifest document into the business type. Every
// path must stay inside the Profile: sync deletes what the manifest lists, so
// a damaged manifest must not point it elsewhere.
func toBusManifest(fm fileManifest) (syncbus.Manifest, error) {
	var m syncbus.Manifest
	for _, f := range fm.Files {
		if !filepath.IsLocal(f.Path) || path.Clean(f.Path) != f.Path {
			return syncbus.Manifest{}, fmt.Errorf("path %q is not a clean relative path inside the Profile", f.Path)
		}
		m.Files = append(m.Files, syncbus.OwnedFile{Path: f.Path})
	}
	for _, b := range fm.Blocks {
		if !filepath.IsLocal(b.Path) || path.Clean(b.Path) != b.Path {
			return syncbus.Manifest{}, fmt.Errorf("path %q is not a clean relative path inside the Profile", b.Path)
		}
		m.Blocks = append(m.Blocks, syncbus.OwnedBlock{Path: b.Path, Sum: b.SHA256})
	}
	for _, k := range fm.Keys {
		// One path may leave the Profile: ~/.claude.json, the file of the
		// default Profile. It is only ever edited key by key, never deleted.
		if k.Path != "../.claude.json" && (!filepath.IsLocal(k.Path) || path.Clean(k.Path) != k.Path) {
			return syncbus.Manifest{}, fmt.Errorf("path %q is not a clean relative path inside the Profile", k.Path)
		}
		m.Keys = append(m.Keys, syncbus.OwnedKey{Path: k.Path, Key: k.Key, Item: k.Item, Sum: k.SHA256})
	}
	return m, nil
}

// QueryManifest reads ~/.baton/manifests/<profile>.json. A missing file is an
// empty manifest.
func (s *Store) QueryManifest(_ context.Context, profile profilename.Name) (syncbus.Manifest, error) {
	data, err := os.ReadFile(s.manifestPath(profile))
	if os.IsNotExist(err) {
		return syncbus.Manifest{}, nil
	}
	if err != nil {
		return syncbus.Manifest{}, err
	}

	var fm fileManifest
	if err := json.Unmarshal(data, &fm); err != nil {
		return syncbus.Manifest{}, fmt.Errorf("%s: %w", s.manifestPath(profile), err)
	}
	if fm.Version != manifestVersion {
		return syncbus.Manifest{}, fmt.Errorf("%s: unsupported manifest version %d", s.manifestPath(profile), fm.Version)
	}
	m, err := toBusManifest(fm)
	if err != nil {
		return syncbus.Manifest{}, fmt.Errorf("%s: %w", s.manifestPath(profile), err)
	}
	return m, nil
}

// QueryExecutable reports whether path is a regular file with an execute bit.
func (s *Store) QueryExecutable(_ context.Context, path string) (bool, error) {
	info, err := os.Stat(path)
	switch {
	case os.IsNotExist(err):
		return false, nil
	case err != nil:
		return false, err
	}
	return info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0, nil
}
