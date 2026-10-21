// Package syncdocker is the storage of sync for Profiles that live inside a
// container of a Docker Compose service. A named volume is not writable from
// the host, so everything goes through Docker (DESIGN.md, ticket 16):
//
//   - a running service is reached with `docker compose exec -T`, which runs as
//     the service's own user, so the files belong to it;
//   - a stopped service is reached through `docker cp` (reading) and a
//     one-shot container that mounts the volume and runs as the container's
//     user (writing). The service is never started or recreated.
//
// Nothing needs anything in the image but sh, tar and the POSIX tools around
// them. JSON is merged on the host; only finished files are written.
package syncdocker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/loadkpi/baton/foundation/docker"
)

// Service is one Compose service whose container holds Profiles. It looks the
// container up once and shares the answer with all its Profiles.
type Service struct {
	// label names the Project and the service in every error.
	label   string
	compose docker.Compose
	client  docker.Client

	opened bool
	err    error
	box    docker.Container
	home   string
}

// NewService creates the access to the service of a Project.
func NewService(client docker.Client, project string, composeFiles []string, service string) *Service {
	return &Service{
		label:   fmt.Sprintf("project %s, service %s", project, service),
		compose: docker.Compose{Client: client, Files: composeFiles, Service: service},
		client:  client,
	}
}

// errorf is an error that names the Project and the service.
func (s *Service) errorf(format string, a ...any) error {
	return fmt.Errorf("%s: %w", s.label, fmt.Errorf(format, a...))
}

// open finds the container, once.
func (s *Service) open(ctx context.Context) error {
	if s.opened {
		return s.err
	}
	s.opened = true
	s.err = s.find(ctx)
	return s.err
}

func (s *Service) find(ctx context.Context) error {
	ids, err := s.compose.ContainerIDs(ctx)
	if err != nil {
		return s.errorf("list the container: %w", err)
	}
	switch len(ids) {
	case 0:
		return s.errorf("the service has no container; create it once (docker compose create) so that its volumes can be reached")
	case 1:
	default:
		return s.errorf("the service has %d containers; Baton syncs one", len(ids))
	}
	if s.box, err = s.client.Inspect(ctx, ids[0]); err != nil {
		return s.errorf("inspect the container: %w", err)
	}
	if s.box.Running {
		out, err := s.compose.Exec(ctx, nil, "sh", "-c", `printf %s "$HOME"`)
		if err != nil {
			return s.errorf("read HOME in the container: %w", err)
		}
		s.home = string(out)
	} else if s.home, err = s.homeOfStopped(ctx); err != nil {
		return s.errorf("find the home directory of the container's user: %w", err)
	}
	if !path.IsAbs(s.home) {
		return s.errorf("the home directory of the container's user is %q, not an absolute path", s.home)
	}
	return nil
}

// Home is the HOME the Agents in the container see. It fails when the service
// cannot be reached; the error names the Project and the service.
func (s *Service) Home(ctx context.Context) (string, error) {
	if err := s.open(ctx); err != nil {
		return "", err
	}
	return s.home, nil
}

// CodexHome is the directory Codex keeps its files in inside the container:
// CODEX_HOME of the container's environment, else .codex in the HOME of its
// user. The directory itself may be a link into a volume; Profile.Check
// follows it. A CODEX_HOME that is not an absolute path is returned as it is,
// for Check to refuse.
func (s *Service) CodexHome(ctx context.Context) (string, error) {
	if err := s.open(ctx); err != nil {
		return "", err
	}
	var value string
	if s.box.Running {
		var err error
		if value, err = s.compose.Getenv(ctx, "CODEX_HOME"); err != nil {
			return "", s.errorf("read CODEX_HOME in the container: %w", err)
		}
	} else {
		for _, kv := range s.box.Env {
			if v, ok := strings.CutPrefix(kv, "CODEX_HOME="); ok {
				value = v
			}
		}
	}
	if value == "" {
		return path.Join(s.home, ".codex"), nil
	}
	return value, nil
}

// homeOfStopped finds HOME of a container that is not running: the
// environment it was created with, else the passwd entry of its user.
func (s *Service) homeOfStopped(ctx context.Context) (string, error) {
	for _, kv := range s.box.Env {
		if v, ok := strings.CutPrefix(kv, "HOME="); ok && v != "" {
			return v, nil
		}
	}
	user, _, _ := strings.Cut(s.box.User, ":")
	if user == "" {
		user = "root"
	}
	data, err := s.client.CopyFrom(ctx, s.box.ID, "/etc/passwd")
	if err != nil {
		return "", err
	}
	body, err := tarFile(data)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(body), "\n") {
		f := strings.Split(line, ":")
		if len(f) >= 6 && (f[0] == user || f[2] == user) {
			return f[5], nil
		}
	}
	return "", fmt.Errorf("user %q is not in the container's /etc/passwd", user)
}

// tarFile returns the content of the one regular file in a tar stream.
func tarFile(data []byte) ([]byte, error) {
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		h, err := tr.Next()
		if err != nil {
			return nil, fmt.Errorf("read the tar stream: %w", err)
		}
		if h.Typeflag == tar.TypeReg {
			return io.ReadAll(tr)
		}
	}
}

// The scripts run in the container with `sh -c`. Paths are positional
// parameters, never part of the text, so nothing in a name can be taken for
// shell syntax.
const (
	// headScript says what is at $1 without following a symbolic link.
	headScript = `if [ -L "$1" ]; then printf 'link\n'; readlink -- "$1"; elif [ -d "$1" ]; then printf 'dir\n'; elif [ -f "$1" ]; then printf 'file\n'; elif [ -e "$1" ]; then printf 'other\n'; else printf 'missing\n'; fi`

	// dirScript prints the real path of the directory $1, following links.
	dirScript = `[ -d "$1" ] && readlink -f -- "$1"`

	// readScript streams $3 (a name in directory $2) as a tar archive, and
	// nothing when $1 is not there. tar stores a symbolic link as a link.
	readScript = `if [ -e "$1" ] || [ -L "$1" ]; then exec tar -C "$2" -cf - "./$3"; fi`

	// writeScript writes stdin to $1 atomically: a temporary name in the same
	// directory, mode $2, then rename. The file is private from the start.
	writeScript = `d=$(dirname -- "$1") || exit 1
mkdir -p -- "$d" || exit 1
umask 077
t=$(mktemp "$d/.baton-tmp.XXXXXX") || exit 1
if cat > "$t" && chmod "$2" "$t" && mv -f -- "$t" "$1"; then exit 0; fi
rm -f -- "$t"
exit 1`

	// removeFileScript removes the file $1, then the directories it leaves
	// empty up to, not including, $2.
	removeFileScript = `case "$1" in "$2"/*) ;; *) exit 2;; esac
if [ -d "$1" ] && [ ! -L "$1" ]; then exit 3; fi
rm -f -- "$1" || exit 1
d=$(dirname -- "$1")
while [ "$d" != "$2" ] && rmdir -- "$d" 2>/dev/null; do d=$(dirname -- "$d"); done
exit 0`

	// removeAllScript removes $1 and everything below it; a symbolic link is
	// removed itself, not its target.
	removeAllScript = `rm -rf -- "$1"`

	// removeOneScript removes the file $1 if it is there.
	removeOneScript = `rm -f -- "$1"`
)

// head says what is at p: "dir", "file", "link" (with the link's target),
// "other" or "missing". A symbolic link at p is not followed.
func (s *Service) head(ctx context.Context, p string) (kind, target string, err error) {
	if s.box.Running {
		out, err := s.compose.Exec(ctx, nil, "sh", "-c", headScript, "sh", p)
		if err != nil {
			return "", "", err
		}
		kind, target, _ = strings.Cut(strings.TrimRight(string(out), "\n"), "\n")
		return kind, target, nil
	}
	h, err := s.client.CopyHead(ctx, s.box.ID, p)
	switch {
	case docker.IsMissing(err):
		return "missing", "", nil
	case err != nil:
		return "", "", err
	}
	switch h.Typeflag {
	case tar.TypeDir:
		return "dir", "", nil
	case tar.TypeReg:
		return "file", "", nil
	case tar.TypeSymlink:
		return "link", h.Linkname, nil
	default:
		return "other", "", nil
	}
}

// resolve follows the symbolic links of the last element of p until it reaches
// something that is not one, and returns that path.
func (s *Service) resolve(ctx context.Context, p string) (string, error) {
	if s.box.Running {
		out, err := s.compose.Exec(ctx, nil, "readlink", "-f", "--", p)
		return strings.TrimRight(string(out), "\n"), err
	}
	// The container is stopped: follow the links one `docker cp` at a time.
	const maxLinks = 40
	for range maxLinks {
		kind, target, err := s.head(ctx, p)
		if err != nil {
			return "", err
		}
		if kind != "link" {
			return p, nil
		}
		if path.IsAbs(target) {
			p = path.Clean(target)
		} else {
			p = path.Join(path.Dir(p), target)
		}
	}
	return "", fmt.Errorf("%s: too many levels of symbolic links", p)
}

// readTar returns the tar archive of what is at p (nil when nothing is).
func (s *Service) readTar(ctx context.Context, p string) ([]byte, error) {
	if s.box.Running {
		out, err := s.compose.Exec(ctx, nil, "sh", "-c", readScript, "sh", p, path.Dir(p), path.Base(p))
		if err != nil {
			return nil, err
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	}
	out, err := s.client.CopyFrom(ctx, s.box.ID, p)
	if docker.IsMissing(err) {
		return nil, nil
	}
	return out, err
}

// run runs a script that changes files. paths are the first positional
// parameters, rest the others. In a stopped service the paths are given as
// the volume sees them, in a one-shot container that runs as the container's
// user; in a running one the script runs in the service itself.
func (s *Service) run(ctx context.Context, stdin io.Reader, script string, paths []string, rest ...string) error {
	if s.box.Running {
		args := append(append([]string{"sh", "-c", script, "sh"}, paths...), rest...)
		_, err := s.compose.Exec(ctx, stdin, args...)
		return err
	}

	var shot docker.OneShot
	inside := make([]string, len(paths))
	for i, p := range paths {
		m, sub, err := s.mountOf(p)
		if err != nil {
			return err
		}
		if i == 0 {
			shot = docker.OneShot{Image: s.box.Image, User: s.box.User, Volume: m, MountPoint: "/v"}
		} else if m != shot.Volume {
			return fmt.Errorf("%s and %s are in different volumes", paths[0], p)
		}
		inside[i] = path.Join("/v", sub)
	}
	_, err := s.client.RunOnce(ctx, shot, stdin, script, append(inside, rest...)...)
	return err
}

// mountOf says where a path of a stopped container can be written: the volume
// (or the host directory of a bind mount) that holds it, and the path in it.
func (s *Service) mountOf(p string) (volume, sub string, err error) {
	best := -1
	for i, m := range s.box.Mounts {
		if m.Type != "volume" && m.Type != "bind" {
			continue
		}
		if p != m.Destination && !strings.HasPrefix(p, strings.TrimSuffix(m.Destination, "/")+"/") {
			continue
		}
		if best < 0 || len(m.Destination) > len(s.box.Mounts[best].Destination) {
			best = i
		}
	}
	if best < 0 {
		return "", "", s.errorf("%s is not in a volume or a bind mount, so it cannot be written while the service is stopped; start the service", p)
	}
	m := s.box.Mounts[best]
	rel := strings.TrimPrefix(p, strings.TrimSuffix(m.Destination, "/"))
	volume = m.Name
	if m.Type == "bind" {
		volume = m.Source
	}
	return volume, path.Clean("/" + rel), nil
}

// hostPath maps a container path to the host path it stands for when it is in
// a bind mount, else "".
func (s *Service) hostPath(p string) string {
	for _, m := range s.box.Mounts {
		if m.Type != "bind" {
			continue
		}
		if p == m.Destination || strings.HasPrefix(p, strings.TrimSuffix(m.Destination, "/")+"/") {
			return path.Join(m.Source, strings.TrimPrefix(p, m.Destination))
		}
	}
	return ""
}

// inNamedVolume is true when p is in a named volume (not a bind mount).
func (s *Service) inNamedVolume(p string) bool {
	best := -1
	for i, m := range s.box.Mounts {
		if (m.Type != "volume" && m.Type != "bind") || (p != m.Destination && !strings.HasPrefix(p, strings.TrimSuffix(m.Destination, "/")+"/")) {
			continue
		}
		if best < 0 || len(m.Destination) > len(s.box.Mounts[best].Destination) {
			best = i
		}
	}
	return best >= 0 && s.box.Mounts[best].Type == "volume"
}
