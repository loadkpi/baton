// Command dockerfake plays the few parts of docker that Baton uses to reach
// the files of a Compose service: `compose ps`, `compose exec`, `compose up -d`, `inspect`,
// `volume inspect`, `cp` and `run -v`. The containers' file systems are
// directories of the host; the first argument is a JSON file that lists them
// (see internal/batontest/compose.go, which writes it).
//
// It is meant to be started by the recording `docker` stub of an Env, which
// has already noted the call. Commands that run inside a container are run for
// real on the host with every absolute path argument moved under the
// container's root and the root cut out of what they print, so the scripts
// Baton sends are the scripts that run. Symbolic links inside a root point to
// host paths under it; the fixtures make them.
package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type state struct {
	Containers     []container `json:"containers"`
	MissingVolumes []string    `json:"missing_volumes"`
}

type container struct {
	ID      string   `json:"id"`
	Service string   `json:"service"`
	Running bool     `json:"running"`
	User    string   `json:"user"`
	Image   string   `json:"image"`
	Home    string   `json:"home"`
	Root    string   `json:"root"`
	Env     []string `json:"env"`
	Volumes []volume `json:"volumes"`
	Binds   []bind   `json:"binds"`
}

type volume struct {
	Name string `json:"name"`
	Dest string `json:"dest"`
}

type bind struct {
	Source   string `json:"source"`
	Dest     string `json:"dest"`
	ReadOnly bool   `json:"read_only"`
}

func main() {
	if len(os.Args) < 3 {
		fail(2, "dockerfake: usage: dockerfake STATE ARGS...")
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fail(2, "dockerfake: %v", err)
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		fail(2, "dockerfake: %v", err)
	}
	args := os.Args[2:]
	switch args[0] {
	case "compose":
		compose(os.Args[1], st, args[1:])
	case "inspect":
		inspect(st, args[len(args)-1])
	case "volume":
		volumeInspect(st, args[len(args)-1])
	case "cp":
		cp(st, args[1])
	case "run":
		run(st, args[1:])
	default:
		fail(1, "dockerfake: unsupported command %q", args[0])
	}
}

func fail(code int, format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(code)
}

func (st state) byService(svc string) []container {
	var out []container
	for _, c := range st.Containers {
		if c.Service == svc {
			out = append(out, c)
		}
	}
	return out
}

func (st state) byID(id string) (container, bool) {
	for _, c := range st.Containers {
		if c.ID == id {
			return c, true
		}
	}
	return container{}, false
}

// compose handles `-f FILE ... ps -a -q SERVICE`, `... up -d SERVICE` and
// `... exec [-T] [-w DIR] SERVICE ARGV`.
func compose(statePath string, st state, args []string) {
	for len(args) >= 2 && (args[0] == "-f" || args[0] == "-p" || args[0] == "--project-directory") {
		args = args[2:]
	}
	if len(args) == 0 {
		fail(1, "dockerfake: compose: no subcommand")
	}
	switch args[0] {
	case "ps":
		all := false
		var svc string
		for _, a := range args[1:] {
			switch a {
			case "-a":
				all = true
			case "-q":
			default:
				svc = a
			}
		}
		for _, c := range st.byService(svc) {
			if all || c.Running {
				fmt.Println(c.ID)
			}
		}
	case "up":
		up(statePath, st, args[1:])
	case "exec":
		rest := args[1:]
		dir := ""
		for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
			switch rest[0] {
			case "-T":
				rest = rest[1:]
			case "-w":
				if len(rest) < 2 {
					fail(1, "dockerfake: compose exec: -w needs a directory")
				}
				dir, rest = rest[1], rest[2:]
			default:
				fail(1, "dockerfake: compose exec: unsupported flag %q", rest[0])
			}
		}
		if len(rest) < 2 {
			fail(1, "dockerfake: compose exec: want SERVICE COMMAND")
		}
		found := st.byService(rest[0])
		if len(found) == 0 || !found[0].Running {
			fail(1, "service %q is not running", rest[0])
		}
		c := found[0]
		execIn(c, dir, rest[1:])
	default:
		fail(1, "dockerfake: compose: unsupported subcommand %q", args[0])
	}
}

// up handles `up -d SERVICE`: the service's container runs afterwards. The new
// state is written back, for the calls that follow.
func up(statePath string, st state, args []string) {
	detached := false
	var svc string
	for _, a := range args {
		switch a {
		case "-d":
			detached = true
		case "--force-recreate":
			fail(1, "dockerfake: compose up: --force-recreate is not played")
		default:
			svc = a
		}
	}
	if !detached || svc == "" {
		fail(1, "dockerfake: compose up: want -d SERVICE")
	}
	found := false
	for i := range st.Containers {
		if st.Containers[i].Service == svc {
			st.Containers[i].Running, found = true, true
		}
	}
	if !found {
		fail(1, "no such service: %s", svc)
	}
	data, err := json.Marshal(st)
	if err != nil {
		fail(2, "dockerfake: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0o644); err != nil {
		fail(2, "dockerfake: %v", err)
	}
}

func inspect(st state, id string) {
	c, ok := st.byID(id)
	if !ok {
		fail(1, "Error: No such container: %s", id)
	}
	type mount struct {
		Type, Name, Source, Destination string
		RW                              bool
	}
	var mounts []mount
	for _, v := range c.Volumes {
		mounts = append(mounts, mount{"volume", v.Name, "/var/lib/docker/volumes/" + v.Name + "/_data", v.Dest, true})
	}
	for _, b := range c.Binds {
		mounts = append(mounts, mount{"bind", "", b.Source, b.Dest, !b.ReadOnly})
	}
	doc := []map[string]any{{
		"Id":     c.ID,
		"State":  map[string]any{"Running": c.Running},
		"Image":  "sha256:" + c.ID,
		"Config": map[string]any{"User": c.User, "Image": c.Image, "Env": c.Env},
		"Mounts": mounts,
	}}
	_ = json.NewEncoder(os.Stdout).Encode(doc)
}

func volumeInspect(st state, name string) {
	for _, gone := range st.MissingVolumes {
		if gone == name {
			fail(1, "Error response from daemon: get %s: no such volume", name)
		}
	}
	for _, c := range st.Containers {
		for _, v := range c.Volumes {
			if v.Name == name {
				fmt.Println("[]")
				return
			}
		}
	}
	fail(1, "Error response from daemon: get %s: no such volume", name)
}

// cp prints the tar archive of CONTAINER:PATH, which holds a symbolic link at
// PATH as a link.
func cp(st state, spec string) {
	id, p, ok := strings.Cut(spec, ":")
	c, found := st.byID(id)
	if !ok || !found {
		fail(1, "Error response from daemon: No such container: %s", id)
	}
	host := c.Root + p
	if _, err := os.Lstat(host); err != nil {
		fail(1, "Error response from daemon: Could not find the file %s in container %s", p, id)
	}
	cmd := exec.Command("tar", "-C", filepath.Dir(host), "-cf", "-", filepath.Base(host))
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fail(1, "dockerfake: tar: %v", err)
	}
	emit(out.Bytes(), c.Root)
}

// run handles the one-shot container Baton writes through:
// run --rm -i --network=none --pull=never [--user U] --entrypoint sh -v VOL:/v IMAGE -c SCRIPT sh ARGS...
func run(st state, args []string) {
	var vol, mountPoint, image string
	i := 0
	for ; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--user" || a == "--entrypoint":
			i++
		case a == "-v":
			i++
			vol, mountPoint, _ = strings.Cut(args[i], ":")
		case strings.HasPrefix(a, "-"):
		default:
			image = a
		}
		if image != "" {
			i++
			break
		}
	}
	hostDir := ""
	for _, c := range st.Containers {
		for _, v := range c.Volumes {
			if v.Name == vol {
				hostDir = c.Root + v.Dest
			}
		}
		for _, b := range c.Binds {
			if b.Source == vol {
				hostDir = c.Root + b.Dest
			}
		}
	}
	if hostDir == "" || image == "" || len(args[i:]) < 3 || args[i] != "-c" {
		fail(125, "dockerfake: run: cannot play %v", args)
	}
	cmd := []string{"sh", "-c", args[i+1], args[i+2]}
	for _, a := range args[i+3:] {
		if a == mountPoint || strings.HasPrefix(a, mountPoint+"/") {
			a = hostDir + strings.TrimPrefix(a, mountPoint)
		}
		cmd = append(cmd, a)
	}
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdin, c.Stderr = os.Stdin, os.Stderr
	var out bytes.Buffer
	c.Stdout = &out
	code := exitCode(c.Run())
	emit(out.Bytes(), hostDir)
	os.Exit(code)
}

// execIn runs argv in dir (the container's default, its root, when empty) with
// every absolute path argument moved under the container's root. Like docker,
// the process gets the environment the container was created with, plus HOME,
// and nothing of the caller's; PATH and TMPDIR of the host stay so that the
// host's tools run.
func execIn(c container, dir string, argv []string) {
	cmd := make([]string, len(argv))
	for i, a := range argv {
		cmd[i] = a
		if strings.HasPrefix(a, "/") {
			cmd[i] = c.Root + a
		}
	}
	e := exec.Command(cmd[0], cmd[1:]...)
	e.Dir = c.Root + dir
	if dir == "" {
		e.Dir = c.Root
	}
	env := append([]string{"PATH=" + os.Getenv("PATH"), "TMPDIR=" + os.Getenv("TMPDIR")}, c.Env...)
	e.Env = append(env, "HOME="+c.Root+c.Home)
	e.Stdin, e.Stderr = os.Stdin, os.Stderr
	var out bytes.Buffer
	e.Stdout = &out
	code := exitCode(e.Run())
	emit(out.Bytes(), c.Root)
	os.Exit(code)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	fmt.Fprintln(os.Stderr, "dockerfake:", err)
	return 127
}

// emit writes what a command printed, with the root of the container cut out
// of paths: in text, and in the link targets of a tar archive.
func emit(out []byte, root string) {
	if len(out) >= 512 && string(out[257:262]) == "ustar" {
		_ = rewriteTar(os.Stdout, out, root)
		return
	}
	_, _ = os.Stdout.Write(bytes.ReplaceAll(out, []byte(root), nil))
}

func rewriteTar(w io.Writer, data []byte, root string) error {
	tr := tar.NewReader(bytes.NewReader(data))
	tw := tar.NewWriter(w)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return tw.Close()
		}
		if err != nil {
			return err
		}
		h.Linkname = strings.TrimPrefix(h.Linkname, root)
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return err
		}
	}
}
