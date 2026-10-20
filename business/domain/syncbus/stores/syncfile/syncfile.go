// Package syncfile is the file storage of the sync domain.
package syncfile

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/loadkpi/baton/business/domain/syncbus"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/foundation/atomicfile"
)

// Store reads the source repository, writes Profile directories and keeps
// the manifests in the Baton state directory.
type Store struct {
	batonDir string
}

// New creates the sync storage. batonDir is the Baton state directory
// (~/.baton).
func New(batonDir string) *Store {
	return &Store{batonDir: batonDir}
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

// QuerySkills reads every skill directory under <source>/skills. Entries
// starting with "." and plain files are ignored; a directory without SKILL.md
// is reported as skipped.
func (s *Store) QuerySkills(_ context.Context, source string) (syncbus.SourceSkills, error) {
	if err := checkDir(source); err != nil {
		return syncbus.SourceSkills{}, fmt.Errorf("source repository: %w", err)
	}

	root := filepath.Join(source, "skills")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return syncbus.SourceSkills{}, nil
	}
	if err != nil {
		return syncbus.SourceSkills{}, err
	}

	var res syncbus.SourceSkills
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		files, err := readFiles(filepath.Join(root, e.Name()))
		if err != nil {
			return syncbus.SourceSkills{}, err
		}
		if !slices.ContainsFunc(files, func(f syncbus.File) bool { return f.Path == "SKILL.md" }) {
			res.Skipped = append(res.Skipped, e.Name())
			continue
		}
		res.Skills = append(res.Skills, syncbus.Skill{Name: e.Name(), Files: files})
	}
	return res, nil
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

// WriteFile writes rel (slash-separated) inside profileDir atomically, unless
// the file is already as wanted.
func (s *Store) WriteFile(_ context.Context, profileDir, rel string, data []byte, mode fs.FileMode) (bool, error) {
	return atomicfile.WriteIfChanged(filepath.Join(profileDir, filepath.FromSlash(rel)), data, mode)
}

// fileManifest is the JSON document of a manifest. Primitive types only.
//
// Entries are objects, not bare strings, so later versions can add fields
// (owned blocks and keys of shared files) without breaking this format.
type fileManifest struct {
	Version int         `json:"version"`
	Profile string      `json:"profile"`
	Files   []fileOwned `json:"files"`
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

// FileExists reports whether rel (slash-separated) exists inside profileDir.
// A symbolic link counts as existing, even a dangling one.
func (s *Store) FileExists(_ context.Context, profileDir, rel string) (bool, error) {
	_, err := os.Lstat(filepath.Join(profileDir, filepath.FromSlash(rel)))
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	default:
		return false, err
	}
}

// toBusManifest converts the manifest document into the business type.
func toBusManifest(fm fileManifest) syncbus.Manifest {
	var m syncbus.Manifest
	for _, f := range fm.Files {
		m.Files = append(m.Files, syncbus.OwnedFile{Path: f.Path})
	}
	return m
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
	return toBusManifest(fm), nil
}
