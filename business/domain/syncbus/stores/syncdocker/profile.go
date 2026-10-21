package syncdocker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/loadkpi/baton/business/domain/syncbus"
	"github.com/loadkpi/baton/foundation/git"
)

// Profile is the files of one Profile inside the container of a Service. It
// implements syncbus.ProfileFiles.
//
// dir is the Profile's directory as the Agent names it ("/home/dev/.claude"),
// which may be a symbolic link into a volume. Paths are built from it
// lexically, as the Agent does (so "../.claude.json" is the file next to the
// link, not next to the volume's directory); the real directory is looked up
// by Check, from inside the container.
type Profile struct {
	svc *Service
	dir string
	// real is dir with its symbolic links followed.
	real string
}

// Profile returns the files of the Profile whose directory is dir, an
// absolute path in the container.
func (s *Service) Profile(dir string) *Profile {
	return &Profile{svc: s, dir: path.Clean(dir)}
}

var _ syncbus.ProfileFiles = (*Profile)(nil)

// Check finds the container and the Profile's real directory in it. It fails
// when the service has no container, when the directory is not there (sync
// never creates a Profile) and, for a stopped service, when the directory is
// not in a volume that can be written.
func (p *Profile) Check(ctx context.Context) error {
	s := p.svc
	if err := s.open(ctx); err != nil {
		return err
	}
	if !path.IsAbs(p.dir) {
		return s.errorf("the directory of the Profile is %q, not an absolute path in the container", p.dir)
	}
	if s.box.Running {
		out, err := s.compose.Exec(ctx, nil, "sh", "-c", dirScript, "sh", p.dir)
		if err != nil {
			return s.errorf("directory %s does not exist in the container", p.dir)
		}
		p.real = strings.TrimRight(string(out), "\n")
		return nil
	}

	real, err := s.resolve(ctx, p.dir)
	if err != nil {
		return s.errorf("find %s: %w", p.dir, err)
	}
	kind, _, err := s.head(ctx, real)
	if err != nil {
		return s.errorf("find %s: %w", real, err)
	}
	if kind != "dir" {
		return s.errorf("directory %s does not exist in the container", p.dir)
	}
	volume, _, err := s.mountOf(real)
	if err != nil {
		return err
	}
	if s.inNamedVolume(real) {
		ok, err := s.client.VolumeExists(ctx, volume)
		if err != nil {
			return s.errorf("look up volume %s: %w", volume, err)
		}
		if !ok {
			return s.errorf("volume %s of %s does not exist", volume, real)
		}
	}
	p.real = real
	return nil
}

// Path is rel inside the Profile directory, in the container. The skills of
// Codex are the exception: Codex reads them from $HOME/.agents/skills, which is
// next to its directory only when that is $HOME/.codex, not when CODEX_HOME
// points elsewhere.
func (p *Profile) Path(rel string) string {
	if rest, ok := strings.CutPrefix(rel, syncbus.CodexSkillsRel); ok && p.svc.home != "" && (rest == "" || rest[0] == '/') {
		return path.Join(p.svc.home, ".agents/skills", rest)
	}
	return path.Join(p.dir, rel)
}

// onDisk is where the bytes of a Profile path really are, for a stopped
// service, whose one-shot container sees the volume and not the links of the
// container's file system.
func (p *Profile) onDisk(logical string) string {
	switch {
	case p.real == "" || p.real == p.dir:
		return logical
	case logical == p.dir:
		return p.real
	}
	if rest, ok := strings.CutPrefix(logical, p.dir+"/"); ok {
		return path.Join(p.real, rest)
	}
	return logical
}

// QueryEntry reads what is at rel without following a symbolic link at rel.
func (p *Profile) QueryEntry(ctx context.Context, rel string) (syncbus.Entry, error) {
	return p.entryAt(ctx, p.Path(rel))
}

func (p *Profile) entryAt(ctx context.Context, full string) (syncbus.Entry, error) {
	data, err := p.svc.readTar(ctx, full)
	if err != nil {
		return syncbus.Entry{}, p.svc.errorf("read %s: %w", full, err)
	}
	if data == nil {
		return syncbus.Entry{Kind: syncbus.Missing}, nil
	}
	entry, err := entryOfTar(data)
	if err != nil {
		return syncbus.Entry{}, p.svc.errorf("read %s: %w", full, err)
	}
	return entry, nil
}

// QueryShared reads rel and follows a symbolic link at rel to the file behind
// it.
func (p *Profile) QueryShared(ctx context.Context, rel string) (syncbus.SharedFile, error) {
	full := p.Path(rel)
	entry, err := p.entryAt(ctx, full)
	if err != nil {
		return syncbus.SharedFile{}, err
	}
	file := syncbus.SharedFile{Exists: true, Path: full}
	switch entry.Kind {
	case syncbus.Missing:
		return syncbus.SharedFile{Path: full}, nil
	case syncbus.Symlink:
		if file.Path, err = p.svc.resolve(ctx, full); err != nil {
			return syncbus.SharedFile{}, p.svc.errorf("%s: %w", full, err)
		}
		file.Linked = true
		if entry, err = p.entryAt(ctx, file.Path); err != nil {
			return syncbus.SharedFile{}, err
		}
		if entry.Kind == syncbus.Missing {
			return syncbus.SharedFile{}, p.svc.errorf("%s is a link to %s, which does not exist", full, file.Path)
		}
	}
	if entry.Kind != syncbus.RegularFile {
		return syncbus.SharedFile{}, p.svc.errorf("%s is not a regular file", full)
	}
	file.Mode, file.Data = entry.Files[0].Mode, entry.Files[0].Data
	return file, nil
}

// WriteFile writes rel atomically.
func (p *Profile) WriteFile(ctx context.Context, rel string, data []byte, mode fs.FileMode) error {
	return p.write(ctx, p.Path(rel), data, mode)
}

// WriteShared writes the file QueryShared found, atomically.
func (p *Profile) WriteShared(ctx context.Context, full string, data []byte, mode fs.FileMode) error {
	return p.write(ctx, full, data, mode)
}

func (p *Profile) write(ctx context.Context, full string, data []byte, mode fs.FileMode) error {
	err := p.svc.run(ctx, bytes.NewReader(data), writeScript, []string{p.onDisk(full)}, fmt.Sprintf("%o", mode.Perm()))
	if err != nil {
		return p.svc.errorf("write %s: %w", full, err)
	}
	return nil
}

// RemoveFile removes rel, then the directories it leaves empty below the top
// directory of the content, never that directory itself.
func (p *Profile) RemoveFile(ctx context.Context, rel string) error {
	full, top := p.Path(rel), p.Path(syncbus.ContentTop(rel))
	if err := p.svc.run(ctx, nil, removeFileScript, []string{p.onDisk(full), p.onDisk(top)}); err != nil {
		return p.svc.errorf("remove %s: %w", full, err)
	}
	return nil
}

// RemoveShared removes the file QueryShared found.
func (p *Profile) RemoveShared(ctx context.Context, full string) error {
	if err := p.svc.run(ctx, nil, removeOneScript, []string{p.onDisk(full)}); err != nil {
		return p.svc.errorf("remove %s: %w", full, err)
	}
	return nil
}

// RemoveAll removes whatever is at rel; a symbolic link is removed itself.
func (p *Profile) RemoveAll(ctx context.Context, rel string) error {
	full := p.Path(rel)
	if err := p.svc.run(ctx, nil, removeAllScript, []string{p.onDisk(full)}); err != nil {
		return p.svc.errorf("remove %s: %w", full, err)
	}
	return nil
}

// QueryTracked returns those of paths that git tracks. A container's own files
// are in no repository; a bind mount can be one, and is looked at from the
// host.
func (p *Profile) QueryTracked(ctx context.Context, paths []string) ([]string, error) {
	var host, container []string
	for _, c := range paths {
		if h := p.svc.hostPath(p.onDisk(c)); h != "" {
			host, container = append(host, h), append(container, c)
		}
	}
	if len(host) == 0 {
		return nil, nil
	}
	tracked, err := git.Tracked(ctx, host)
	if err != nil {
		return nil, err
	}
	var out []string
	for i, h := range host {
		for _, t := range tracked {
			if t == h {
				out = append(out, container[i])
			}
		}
	}
	return out, nil
}

// entryOfTar turns the archive of one path (a file, a link or a directory with
// everything in it) into an Entry. The first member is the path itself.
func entryOfTar(data []byte) (syncbus.Entry, error) {
	tr := tar.NewReader(bytes.NewReader(data))
	first, err := tr.Next()
	if err != nil {
		return syncbus.Entry{}, fmt.Errorf("read the tar stream: %w", err)
	}
	root := strings.TrimSuffix(strings.TrimPrefix(first.Name, "./"), "/")

	switch first.Typeflag {
	case tar.TypeReg:
		body, err := io.ReadAll(tr)
		if err != nil {
			return syncbus.Entry{}, err
		}
		return syncbus.Entry{Kind: syncbus.RegularFile, Files: []syncbus.File{{Mode: fs.FileMode(first.Mode).Perm(), Data: body}}}, nil
	case tar.TypeSymlink:
		return syncbus.Entry{Kind: syncbus.Symlink, Target: first.Linkname}, nil
	case tar.TypeDir:
	default:
		return syncbus.Entry{Kind: syncbus.Other}, nil
	}

	entry := syncbus.Entry{Kind: syncbus.Directory}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return syncbus.Entry{}, fmt.Errorf("read the tar stream: %w", err)
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(h.Name, "./"), root), "/")
		f := syncbus.File{Path: rel}
		switch h.Typeflag {
		case tar.TypeReg:
			if f.Data, err = io.ReadAll(tr); err != nil {
				return syncbus.Entry{}, err
			}
			f.Mode = fs.FileMode(h.Mode).Perm()
		case tar.TypeSymlink:
			f.Mode, f.Data = fs.ModeSymlink|0o777, []byte(h.Linkname)
		default:
			continue
		}
		entry.Files = append(entry.Files, f)
	}
	sort.Slice(entry.Files, func(i, j int) bool { return entry.Files[i].Path < entry.Files[j].Path })
	return entry, nil
}
