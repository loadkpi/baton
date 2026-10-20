package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// streamTimeout bounds the calls that move file content, which may take longer
// than a question.
const streamTimeout = 2 * time.Minute

// Compose names a service of a Docker Compose setup: the files that define it
// and the service in them.
type Compose struct {
	Client Client
	// Files are the Compose files, in the order of the -f flags.
	Files   []string
	Service string
}

func (c Compose) base() []string {
	args := []string{"compose"}
	for _, f := range c.Files {
		args = append(args, "-f", f)
	}
	return args
}

// ContainerIDs returns the ids of the containers of the service, running or
// not. A service that was never created has none.
func (c Compose) ContainerIDs(ctx context.Context) ([]string, error) {
	out, err := c.Client.exec(ctx, timeout, nil, append(c.base(), "ps", "-a", "-q", c.Service)...)
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// Exec runs argv in the running service with `docker compose exec -T` and
// returns its standard output. stdin is streamed to the command when not nil.
// The command runs as the service's user, in its default working directory.
func (c Compose) Exec(ctx context.Context, stdin io.Reader, argv ...string) ([]byte, error) {
	args := append(c.base(), "exec", "-T", c.Service)
	return c.Client.exec(ctx, streamTimeout, stdin, append(args, argv...)...)
}

// Mount is one mount of a container.
type Mount struct {
	// Type is "volume", "bind", "tmpfs", ...
	Type string
	// Name is the name of a volume.
	Name string
	// Source is the host path of a bind mount, or the volume's directory on
	// the Docker host.
	Source string
	// Destination is the path inside the container.
	Destination string
}

// Container is what `docker inspect` says about a container.
type Container struct {
	ID      string
	Running bool
	// User is the container's configured user ("1001:1001", "dev", or empty
	// for the image's default, which is root).
	User string
	// Image is the image the container was created from.
	Image string
	// Env is the container's environment as "NAME=value".
	Env    []string
	Mounts []Mount
}

// Inspect reads a container.
func (c Client) Inspect(ctx context.Context, container string) (Container, error) {
	out, err := c.exec(ctx, timeout, nil, "inspect", "--type", "container", container)
	if err != nil {
		return Container{}, err
	}
	var docs []struct {
		ID    string
		State struct{ Running bool }
		Image string
		// Config.Image is the name the container was created with, which is
		// what to run again; Image is only the id.
		Config struct {
			User  string
			Image string
			Env   []string
		}
		Mounts []struct {
			Type        string
			Name        string
			Source      string
			Destination string
		}
	}
	if err := json.Unmarshal(out, &docs); err != nil {
		return Container{}, fmt.Errorf("docker inspect %s: %w", container, err)
	}
	if len(docs) != 1 {
		return Container{}, fmt.Errorf("docker inspect %s: expected one container, got %d", container, len(docs))
	}
	d := docs[0]
	got := Container{ID: d.ID, Running: d.State.Running, User: d.Config.User, Image: d.Config.Image, Env: d.Config.Env}
	if got.Image == "" {
		got.Image = d.Image
	}
	for _, m := range d.Mounts {
		got.Mounts = append(got.Mounts, Mount{Type: m.Type, Name: m.Name, Source: m.Source, Destination: m.Destination})
	}
	return got, nil
}

// VolumeExists reports whether a named volume exists. `docker run -v name:/x`
// creates a missing volume instead of failing, so a caller that must not
// create one asks first.
func (c Client) VolumeExists(ctx context.Context, name string) (bool, error) {
	_, err := c.exec(ctx, timeout, nil, "volume", "inspect", name)
	if err == nil {
		return true, nil
	}
	var e *Error
	if errors.As(err, &e) && e.Code != 0 && strings.Contains(strings.ToLower(e.Stderr), "no such volume") {
		return false, nil
	}
	return false, err
}

// CopyFrom returns what `docker cp <container>:<path> -` writes: a tar stream
// of the file, directory or symbolic link at path (a link is copied as a link),
// which works for a container that is not running. The error says
// what docker wrote when the path does not exist; IsMissing recognizes it.
func (c Client) CopyFrom(ctx context.Context, container, path string) ([]byte, error) {
	return c.exec(ctx, streamTimeout, nil, "cp", container+":"+path, "-")
}

// IsMissing reports whether err is `docker cp` failing because the path is not
// in the container.
func IsMissing(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	text := strings.ToLower(e.Stderr)
	return strings.Contains(text, "could not find the file") || strings.Contains(text, "no such file")
}

// OneShot describes a short-lived container that mounts one volume.
type OneShot struct {
	// Image is the image to run; its sh must exist.
	Image string
	// User is the user to run as, as in `docker run --user`; empty for the
	// image's default.
	User string
	// Volume is the volume to mount, by name, or the host directory of a bind
	// mount; it appears at MountPoint.
	Volume     string
	MountPoint string
}

// RunOnce runs `sh -c script sh args...` in a new container that is removed
// afterwards, has no network and mounts one volume, with stdin streamed in. It
// never starts or changes any container of a Compose setup.
func (c Client) RunOnce(ctx context.Context, o OneShot, stdin io.Reader, script string, args ...string) ([]byte, error) {
	argv := []string{"run", "--rm", "-i", "--network=none", "--pull=never"}
	if o.User != "" {
		argv = append(argv, "--user", o.User)
	}
	argv = append(argv, "--entrypoint", "sh", "-v", o.Volume+":"+o.MountPoint, o.Image, "-c", script, "sh")
	return c.exec(ctx, streamTimeout, stdin, append(argv, args...)...)
}

// CopyHead returns the header of the first entry of what `docker cp
// <container>:<path> -` produces, which is the file, directory or symbolic link
// at path itself (a link is not followed). It stops reading there, so a
// directory with a lot in it is not copied. The error is the one CopyFrom
// gives when the path does not exist.
func (c Client) CopyHead(ctx context.Context, container, path string) (*tar.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.binary(), "cp", container+":"+path, "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	header, readErr := tar.NewReader(out).Next()
	if readErr == nil {
		cancel() // enough: do not wait for the rest of a directory
		_ = cmd.Wait()
		return header, nil
	}
	_, _ = io.Copy(io.Discard, out)
	if waitErr := cmd.Wait(); waitErr != nil {
		e := &Error{Command: "cp", Stderr: strings.TrimSpace(stderr.String()), Err: waitErr}
		var exit *exec.ExitError
		if errors.As(waitErr, &exit) {
			e.Code = exit.ExitCode()
		}
		return nil, e
	}
	return nil, fmt.Errorf("docker cp %s:%s: %w", container, path, readErr)
}
