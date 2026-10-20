package batontest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ContainerSpec describes a container of a Compose service in a fake docker.
type ContainerSpec struct {
	// Service is the name of the Compose service.
	Service string
	// Running says whether the container runs. A stopped one is only reached
	// through `docker cp` and a one-shot `docker run -v`.
	Running bool
	// User is the container's configured user ("1001:1001"); empty is root.
	User string
	// Image is the name of the image it was created from; default "app-image".
	Image string
	// Home is the HOME of its user; default "/home/dev".
	Home string
	// Volumes are the named volumes it mounts, by name, at their paths.
	Volumes map[string]string
	// Binds are host directories it mounts read-write, by host path, at
	// their paths.
	Binds map[string]string
	// BindsRO are host directories it mounts read-only.
	BindsRO map[string]string
	// Env is the environment the container was created with, as "NAME=value".
	// Every `compose exec` starts with it, and with nothing of the caller's.
	Env []string
	// Hostname is the host name the container was created with
	// (`hostname:` in a Compose file); the first 12 characters of its id when
	// empty, which is what Docker gives a container that has none.
	Hostname string
	// Created is when the container was created, RFC 3339; a date long
	// before any test Event when empty.
	Created string
}

// Container is the fake file system of a container. It is a directory of the
// host; paths are the paths inside the container. Symbolic links made with
// Symlink point into it and look like the container's own.
type Container struct {
	env  *Env
	spec ContainerSpec
	id   string
	root string
}

// fakeState is the document dockerfake reads.
type fakeState struct {
	Containers     []fakeContainer `json:"containers"`
	MissingVolumes []string        `json:"missing_volumes"`
}

type fakeContainer struct {
	ID       string       `json:"id"`
	Service  string       `json:"service"`
	Running  bool         `json:"running"`
	User     string       `json:"user"`
	Image    string       `json:"image"`
	Home     string       `json:"home"`
	Root     string       `json:"root"`
	Env      []string     `json:"env"`
	Hostname string       `json:"hostname"`
	Created  string       `json:"created"`
	Volumes  []fakeVolume `json:"volumes"`
	Binds    []fakeBind   `json:"binds"`
}

type fakeVolume struct {
	Name string `json:"name"`
	Dest string `json:"dest"`
}

type fakeBind struct {
	Source   string `json:"source"`
	Dest     string `json:"dest"`
	ReadOnly bool   `json:"read_only"`
}

// AddContainer adds a container to the fake docker of the Env and installs
// it as the `docker` stub, which keeps recording every call. The fake plays
// `compose ps`, `compose exec`, `compose up -d`, `inspect`, `volume inspect`,
// `cp` and `run -v` against the container's directory (see dockerfake);
// anything else fails.
func (e *Env) AddContainer(spec ContainerSpec) *Container {
	e.t.Helper()
	if spec.Image == "" {
		spec.Image = "app-image"
	}
	if spec.Home == "" {
		spec.Home = "/home/dev"
	}
	id := strings.Repeat(string(rune('a'+e.containerSeq)), 64)
	e.containerSeq++
	if spec.Hostname == "" {
		spec.Hostname = id[:12]
	}
	if spec.Created == "" {
		spec.Created = "2000-01-01T00:00:00Z"
	}
	c := &Container{env: e, spec: spec, id: id, root: filepath.Join(filepath.Dir(e.stubDir), "containers", id)}
	for _, dir := range []string{spec.Home, "/tmp", "/etc"} {
		c.MkdirAll(dir)
	}
	// The user table `docker cp` reads to find the home of a stopped
	// container's user: root, and "dev" as uid 1001.
	c.WriteFile("/etc/passwd", "root:x:0:0:root:/root:/bin/sh\ndev:x:1001:1001::"+spec.Home+":/bin/sh\n")
	for _, dest := range spec.Volumes {
		c.MkdirAll(dest)
	}
	for _, dest := range spec.Binds {
		c.MkdirAll(dest)
	}
	for _, dest := range spec.BindsRO {
		c.MkdirAll(dest)
	}
	e.containers = append(e.containers, c)
	e.writeFakeDocker()
	return c
}

func (e *Env) writeFakeDocker() {
	e.t.Helper()
	st := fakeState{MissingVolumes: e.missingVolumes}
	for _, c := range e.containers {
		fc := fakeContainer{ID: c.id, Service: c.spec.Service, Running: c.spec.Running, User: c.spec.User, Image: c.spec.Image, Home: c.spec.Home, Root: c.root, Env: c.spec.Env, Hostname: c.spec.Hostname, Created: c.spec.Created}
		for name, dest := range c.spec.Volumes {
			fc.Volumes = append(fc.Volumes, fakeVolume{Name: name, Dest: dest})
		}
		for src, dest := range c.spec.Binds {
			fc.Binds = append(fc.Binds, fakeBind{Source: src, Dest: dest})
		}
		for src, dest := range c.spec.BindsRO {
			fc.Binds = append(fc.Binds, fakeBind{Source: src, Dest: dest, ReadOnly: true})
		}
		st.Containers = append(st.Containers, fc)
	}
	data, err := json.Marshal(st)
	if err != nil {
		e.t.Fatal(err)
	}
	statePath := filepath.Join(filepath.Dir(e.stubDir), "dockerfake.json")
	if err := os.WriteFile(statePath, data, 0o644); err != nil {
		e.t.Fatal(err)
	}
	e.Stub("docker", WithScript("exec "+shellQuote(fakeDockerPath(e.bin))+" "+shellQuote(statePath)+` "$@"`))
}

// RemoveFakeVolume makes the fake docker forget a named volume, as if it had
// been removed behind the containers' back.
func (e *Env) RemoveFakeVolume(name string) {
	e.t.Helper()
	e.missingVolumes = append(e.missingVolumes, name)
	e.writeFakeDocker()
}

// Remove deletes the container from the fake docker, as `docker compose down`
// or a recreate does: its id is never used again. The files stay for the test
// to read.
func (c *Container) Remove() {
	c.env.t.Helper()
	for i, other := range c.env.containers {
		if other == c {
			c.env.containers = append(c.env.containers[:i], c.env.containers[i+1:]...)
			break
		}
	}
	c.env.writeFakeDocker()
}

// Process makes pid run in the container, as /proc/<pid>/stat shows it, with
// the start time in clock ticks since boot. `docker exec` of the fake reads
// these files, so the script Baton sends is the script that runs.
func (c *Container) Process(pid int, comm string, start uint64) {
	c.env.t.Helper()
	c.ProcessInState(pid, comm, "S", start)
}

// ProcessInState is Process for a process in the given state (field 3 of the
// stat line): Z for a zombie, X for a dead one.
func (c *Container) ProcessInState(pid int, comm, state string, start uint64) {
	c.env.t.Helper()
	c.WriteFile(fmt.Sprintf("/proc/%d/stat", pid), fmt.Sprintf("%d (%s) %s 1 %d %d 0 -1 4194560 100 0 0 0 1 1 0 0 20 0 1 0 %d 1000000 500 18446744073709551615\n", pid, comm, state, pid, pid, start))
}

// KillProcess makes pid stop running in the container: its stat file is gone.
func (c *Container) KillProcess(pid int) {
	c.env.t.Helper()
	if err := os.Remove(c.host(fmt.Sprintf("/proc/%d/stat", pid))); err != nil {
		c.env.t.Fatal(err)
	}
}

// ID is the container id the fake docker gives.
func (c *Container) ID() string { return c.id }

// SetRunning starts or stops the container.
func (c *Container) SetRunning(running bool) {
	c.env.t.Helper()
	c.spec.Running = running
	c.env.writeFakeDocker()
}

func (c *Container) host(p string) string { return c.root + filepath.Clean("/"+p) }

// MkdirAll makes a directory inside the container.
func (c *Container) MkdirAll(p string) {
	c.env.t.Helper()
	if err := os.MkdirAll(c.host(p), 0o755); err != nil {
		c.env.t.Fatal(err)
	}
}

// WriteFile creates a file (and its directories) with mode 0644.
func (c *Container) WriteFile(p, content string) {
	c.env.t.Helper()
	c.MkdirAll(filepath.Dir(p))
	if err := os.WriteFile(c.host(p), []byte(content), 0o644); err != nil {
		c.env.t.Fatal(err)
	}
}

// Symlink makes p a symbolic link to target, a path inside the container.
func (c *Container) Symlink(p, target string) {
	c.env.t.Helper()
	c.MkdirAll(filepath.Dir(p))
	if err := os.Symlink(c.host(target), c.host(p)); err != nil {
		c.env.t.Fatal(err)
	}
}

// ReadFile returns the content of a file of the container; a missing file
// fails the test.
func (c *Container) ReadFile(p string) string {
	c.env.t.Helper()
	data, err := os.ReadFile(c.host(p))
	if err != nil {
		c.env.t.Fatal(err)
	}
	return string(data)
}

// Exists reports whether something is at p, a symbolic link itself included.
func (c *Container) Exists(p string) bool {
	_, err := os.Lstat(c.host(p))
	return err == nil
}

// Mode returns the permission bits of the file at p.
func (c *Container) Mode(p string) os.FileMode {
	c.env.t.Helper()
	info, err := os.Stat(c.host(p))
	if err != nil {
		c.env.t.Fatal(err)
	}
	return info.Mode().Perm()
}

// ListDir returns the sorted names in a directory of the container.
func (c *Container) ListDir(p string) []string {
	c.env.t.Helper()
	entries, err := os.ReadDir(c.host(p))
	if err != nil {
		c.env.t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// Chmod changes the permission bits of the file at p.
func (c *Container) Chmod(p string, mode os.FileMode) {
	c.env.t.Helper()
	if err := os.Chmod(c.host(p), mode); err != nil {
		c.env.t.Fatal(err)
	}
}
