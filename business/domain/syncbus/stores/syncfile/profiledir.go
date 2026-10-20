package syncfile

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/loadkpi/baton/business/domain/syncbus"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/foundation/atomicfile"
	"github.com/loadkpi/baton/foundation/git"
)

// ProfileDir is the files of a Profile that sits in a directory of the host
// file system: a host Profile, or one inside a sandbox's home directory.
type ProfileDir struct {
	dir string
	// mustExist is the directory that has to be there before sync writes
	// anything: the Profile directory itself for a host Profile (sync never
	// creates one, so a mistyped path is an error), the home directory for a
	// Profile derived from a sandbox (its own directory may not exist yet).
	mustExist string
}

// NewProfileDir returns the files of the Profile directory dir. mustExist is
// the directory that has to exist for sync to proceed.
func NewProfileDir(dir, mustExist string) *ProfileDir {
	return &ProfileDir{dir: dir, mustExist: mustExist}
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

// Check fails unless the directory sync needs exists.
func (p *ProfileDir) Check(_ context.Context) error {
	return checkDir(p.mustExist)
}

// Path is rel inside the Profile directory.
func (p *ProfileDir) Path(rel string) string {
	return filepath.Join(p.dir, filepath.FromSlash(rel))
}

// QueryEntry reads what is at rel. A symbolic link is reported as such, not
// followed.
func (p *ProfileDir) QueryEntry(_ context.Context, rel string) (syncbus.Entry, error) {
	full := p.Path(rel)
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

// QueryShared reads rel and follows a symbolic link at rel to the file behind
// it.
func (p *ProfileDir) QueryShared(_ context.Context, rel string) (syncbus.SharedFile, error) {
	full := p.Path(rel)
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

// WriteFile writes rel atomically.
func (p *ProfileDir) WriteFile(_ context.Context, rel string, data []byte, mode fs.FileMode) error {
	return atomicfile.Write(p.Path(rel), data, mode)
}

// WriteShared writes the file QueryShared found, atomically.
func (p *ProfileDir) WriteShared(_ context.Context, path string, data []byte, mode fs.FileMode) error {
	return atomicfile.Write(path, data, mode)
}

// RemoveShared removes the file QueryShared found.
func (p *ProfileDir) RemoveShared(_ context.Context, path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// RemoveAll removes whatever is at rel: a directory with its content, or a
// symbolic link itself (never its target).
func (p *ProfileDir) RemoveAll(_ context.Context, rel string) error {
	return os.RemoveAll(p.Path(rel))
}

// QueryTracked returns those of paths that git tracks.
func (p *ProfileDir) QueryTracked(ctx context.Context, paths []string) ([]string, error) {
	return git.Tracked(ctx, paths)
}

// RemoveFile removes rel and then the directories that became empty, up to but
// excluding the top directory of the content: the first path element
// ("skills", "config.d"), or ../.agents/skills for the skills of Codex, which
// sit next to the Profile directory.
func (p *ProfileDir) RemoveFile(_ context.Context, rel string) error {
	full := p.Path(rel)
	if info, err := os.Lstat(full); err == nil && info.IsDir() {
		return fmt.Errorf("%s is a directory, not a file", full)
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}

	top := p.Path(topOf(rel))
	for dir := filepath.Dir(full); dir != top && strings.HasPrefix(dir, top+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if err := os.Remove(dir); err != nil {
			break // not empty (or gone): the directory has other content
		}
	}
	return nil
}

// topOf is the directory of rel that is never removed with the files below it.
func topOf(rel string) string {
	parts := strings.Split(rel, "/")
	if parts[0] == ".." && len(parts) > 3 {
		return strings.Join(parts[:3], "/")
	}
	return parts[0]
}

// SaveBackup writes entry to <baton dir>/backups/<profile>/<stamp>/<rel>. It
// refuses to overwrite anything, so a backup never replaces an older one.
func (s *Store) SaveBackup(_ context.Context, profile profilename.Name, stamp, rel string, entry syncbus.Entry) (string, error) {
	// A path next to the Profile directory ("../.agents/skills/x") is kept
	// inside the backup directory, without the "..".
	rel = strings.TrimPrefix(path.Clean("/"+rel), "/")
	dst := filepath.Join(s.batonDir, "backups", profile.String(), stamp, filepath.FromSlash(rel))
	switch entry.Kind {
	case syncbus.RegularFile:
		if err := writeBackupFile(dst, entry.Files[0]); err != nil {
			return "", err
		}
	case syncbus.Directory:
		if err := os.MkdirAll(dst, 0o700); err != nil {
			return "", err
		}
		for _, f := range entry.Files {
			if err := writeBackupFile(filepath.Join(dst, filepath.FromSlash(f.Path)), f); err != nil {
				return "", err
			}
		}
	case syncbus.Symlink:
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return "", err
		}
		if err := os.Symlink(entry.Target, dst); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("nothing to back up at %s", rel)
	}
	return dst, nil
}

// writeBackupFile writes a regular file, or a symbolic link as a link, with
// parent directories of mode 0700.
func writeBackupFile(dst string, f syncbus.File) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if f.Mode&fs.ModeSymlink != 0 {
		return os.Symlink(string(f.Data), dst)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, f.Mode.Perm()|0o600)
	if err != nil {
		return err
	}
	if _, err := out.Write(f.Data); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
