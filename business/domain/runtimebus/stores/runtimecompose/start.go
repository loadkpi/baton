package runtimecompose

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/domain/runtimebus"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/foundation/docker"
)

// apiKeyVars are cleared inside the container before the Agent starts (spec
// story 30). `docker compose exec` gives the process the environment the
// container was created with, whatever the host's is, and its -e can only set
// a variable, not remove one (the Docker documentation says nothing of
// unsetting; checked Oct 20, 2026, not run), so env(1) in front of the Agent
// is what removes them.
var apiKeyVars = []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"}

// service is a Project's Compose service, with its Compose command.
type service struct {
	project  projectname.Name
	path     string
	settings *configbus.ComposeSettings
	compose  docker.Compose
}

// service finds the Compose service of a registered Project.
func (r *Runtime) service(ctx context.Context, name projectname.Name) (service, error) {
	settings := r.cfg.ComposeOf(name)
	if settings == nil {
		return service{}, fmt.Errorf("project %s has no [projects.%s.compose] table in the config", name, name)
	}
	registered, err := r.projects.All(ctx)
	if err != nil {
		return service{}, err
	}
	for _, p := range registered {
		if p.Name == name {
			return service{
				project:  name,
				path:     p.Path,
				settings: settings,
				compose:  docker.Compose{Client: r.client, Files: composeFiles(r.cfg, p, settings.Files), Service: settings.Service},
			}, nil
		}
	}
	return service{}, fmt.Errorf("unknown project %q", name)
}

// inspected is what looking at a container gave, and when.
type inspected struct {
	box docker.Container
	err error
	at  time.Time
}

// boxTTL is how long what was seen of a container is remembered by HostPath
// and ContainerPath. `baton ls` asks once per Session and is done within it;
// `baton watch` lives for days, and a container that was recreated, or did not
// exist when it first asked, must be looked at again.
const boxTTL = time.Second

// find looks at the service's container, running or not. ok is false when the
// service has none.
func (s service) find(ctx context.Context, c docker.Client) (box docker.Container, ok bool, err error) {
	ids, err := s.compose.ContainerIDs(ctx)
	if err != nil {
		return docker.Container{}, false, fmt.Errorf("project %s, service %s: list the container: %w", s.project, s.settings.Service, err)
	}
	switch len(ids) {
	case 0:
		return docker.Container{}, false, nil
	case 1:
	default:
		return docker.Container{}, false, fmt.Errorf("project %s, service %s: the service has %d containers; Baton uses one", s.project, s.settings.Service, len(ids))
	}
	if box, err = c.Inspect(ctx, ids[0]); err != nil {
		return docker.Container{}, false, fmt.Errorf("project %s, service %s: inspect the container: %w", s.project, s.settings.Service, err)
	}
	return box, true, nil
}

// inspect looks at a Project's container for HostPath and ContainerPath, once
// per boxTTL when it was found.
func (r *Runtime) inspect(ctx context.Context, name projectname.Name) (docker.Container, error) {
	if got, ok := r.boxes[name]; ok && time.Since(got.at) < boxTTL {
		return got.box, got.err
	}
	got := inspected{at: time.Now()}
	svc, err := r.service(ctx, name)
	if err == nil {
		var ok bool
		if got.box, ok, err = svc.find(ctx, r.client); err == nil && !ok {
			err = fmt.Errorf("project %s, service %s: the service has no container", name, svc.settings.Service)
		}
	}
	got.err = err
	if err == nil {
		// A failure is asked again at once: the container may appear.
		r.boxes[name] = got
	}
	return got.box, got.err
}

// HostPath maps a path inside the Project's container, such as the cwd of an
// Event, to the host path of the bind mount that holds it. Nothing assumes the
// two are equal; the container's own mounts say (docker inspect, which also
// works for a container that has stopped).
func (r *Runtime) HostPath(ctx context.Context, project projectname.Name, p string) (string, error) {
	box, err := r.inspect(ctx, project)
	if err != nil {
		return "", err
	}
	return hostPath(box.Mounts, p)
}

// ContainerPath is the path under which the Project's container sees a file of
// the host.
func (r *Runtime) ContainerPath(ctx context.Context, project projectname.Name, p string) (string, error) {
	box, err := r.inspect(ctx, project)
	if err != nil {
		return "", err
	}
	return containerPath(box.Mounts, p)
}

// Start prepares the launch of Claude in the Project's service and describes
// the tab that runs it:
//
//  1. the service is brought up when it is not running (`docker compose up -d
//     <service>`; never a recreate, and a running service is left alone);
//  2. `docker inspect` of its container must show Baton's two mounts; if it
//     does not, the error carries the snippet for the user's override file;
//  3. the Project's directory in the container is found from the container's
//     mounts;
//  4. the tab runs `docker compose exec -w <dir> <service> env -u ... claude`.
//
// Codex is not started in a Compose container yet.
func (r *Runtime) Start(ctx context.Context, req runtimebus.StartRequest) (runtimebus.Tab, error) {
	if req.Agent != agent.Claude {
		return runtimebus.Tab{}, fmt.Errorf("%s does not run in a compose container yet; only claude does", req.Agent)
	}
	svc, err := r.service(ctx, req.Project)
	if err != nil {
		return runtimebus.Tab{}, err
	}

	binDir := filepath.Join(req.Home, ".baton", "bin")
	if info, err := os.Stat(filepath.Join(binDir, "baton")); err != nil || !info.Mode().IsRegular() {
		return runtimebus.Tab{}, fmt.Errorf("%s does not exist; run make install, the status hooks in the container call it", filepath.Join(binDir, "baton"))
	}
	// Docker would make a missing mount source itself, as root.
	projectDir := filepath.Join(req.Home, ".baton", "projects", req.Project.String())
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		return runtimebus.Tab{}, err
	}

	box, err := r.ensureRunning(ctx, svc)
	if err != nil {
		return runtimebus.Tab{}, err
	}
	if err := checkMounts(svc, box, binDir, projectDir); err != nil {
		return runtimebus.Tab{}, err
	}
	dir, err := containerPath(box.Mounts, req.WorkingCopy)
	if err != nil {
		return runtimebus.Tab{}, fmt.Errorf("project %s: where is its directory in the container? %w; mount the checkout into the service", req.Project, err)
	}

	home, err := svc.compose.Exec(ctx, nil, "sh", "-c", `printf %s "$HOME"`)
	if err != nil {
		return runtimebus.Tab{}, fmt.Errorf("project %s, service %s: read HOME in the container: %w", req.Project, svc.settings.Service, err)
	}

	words := append([]string{"docker", "compose"}, filesFlags(svc.compose.Files)...)
	words = append(words, "exec", "-w", dir, svc.settings.Service, "env")
	for _, name := range apiKeyVars {
		words = append(words, "-u", name)
	}
	if profileDir := expandHome(req.Profile.Dir, string(home)); profileDir == "" || path.Clean(profileDir) == path.Join(string(home), ".claude") {
		// Claude Code reads ~/.claude when the variable is unset; one that the
		// container was created with would send it somewhere else.
		words = append(words, "-u", "CLAUDE_CONFIG_DIR")
	} else {
		words = append(words, "CLAUDE_CONFIG_DIR="+profileDir)
	}
	words = append(words, req.Agent.Binary())
	if req.Prompt != "" {
		words = append(words, req.Prompt)
	}

	title := req.Project.String() + "/" + req.Task + ":" + req.Agent.String()
	return runtimebus.Tab{Dir: req.WorkingCopy, Title: title, Command: words}, nil
}

func filesFlags(files []string) []string {
	var out []string
	for _, f := range files {
		out = append(out, "-f", f)
	}
	return out
}

// ensureRunning returns the service's container, running. A service that is
// not running is brought up; one that is running is not touched.
func (r *Runtime) ensureRunning(ctx context.Context, svc service) (docker.Container, error) {
	box, ok, err := svc.find(ctx, r.client)
	if err != nil {
		return docker.Container{}, err
	}
	if ok && box.Running {
		return box, nil
	}
	_, _ = fmt.Fprintf(r.log, "baton: service %s of project %s is not running; docker compose up -d %s\n", svc.settings.Service, svc.project, svc.settings.Service)
	if err := svc.compose.Up(ctx, r.log); err != nil {
		return docker.Container{}, fmt.Errorf("project %s, service %s: bring the service up: %w", svc.project, svc.settings.Service, err)
	}
	if box, ok, err = svc.find(ctx, r.client); err != nil {
		return docker.Container{}, err
	}
	if !ok || !box.Running {
		return docker.Container{}, fmt.Errorf("project %s, service %s: the service is not running after docker compose up -d", svc.project, svc.settings.Service)
	}
	return box, nil
}

// want is a mount a container needs for Baton.
type want struct {
	source, dest string
	readOnly     bool
}

func (w want) volume() string {
	mode := "rw"
	if w.readOnly {
		mode = "ro"
	}
	return w.source + ":" + w.dest + ":" + mode
}

// checkMounts is nil when the container has both Baton mounts as they must be:
// the host's ~/.baton/bin at /baton/bin, read-only, and the Project's
// directory at /baton/project, read-write. Otherwise it is a *MountsError.
func checkMounts(svc service, box docker.Container, binDir, projectDir string) error {
	var problems []string
	var missing []want
	for _, w := range []want{
		{source: binDir, dest: runtimebus.ContainerBinDir, readOnly: true},
		{source: projectDir, dest: runtimebus.ContainerProject},
	} {
		var found *docker.Mount
		for i, m := range box.Mounts {
			if path.Clean(m.Destination) == w.dest {
				found = &box.Mounts[i]
			}
		}
		switch {
		case found == nil || found.Type != "bind":
			problems = append(problems, w.dest+" is not mounted")
		case realPath(found.Source) != realPath(w.source):
			problems = append(problems, fmt.Sprintf("%s is mounted from %s, not from %s", w.dest, found.Source, w.source))
		case found.ReadWrite == w.readOnly:
			if w.readOnly {
				problems = append(problems, w.dest+" is mounted read-write, not read-only")
			} else {
				problems = append(problems, w.dest+" is mounted read-only, not read-write")
			}
		default:
			continue
		}
		missing = append(missing, w)
	}
	if len(problems) == 0 {
		return nil
	}
	return &MountsError{
		Project: svc.project.String(), Service: svc.settings.Service, Files: svc.compose.Files,
		Problems: problems, Snippet: snippet(svc.settings.Service, missing),
	}
}

// MountsError says that the container does not have Baton's mounts and what
// the user adds to their own Compose override file.
type MountsError struct {
	Project, Service string
	// Files are the Compose files of the service, in the order Baton passes them.
	Files    []string
	Problems []string
	Snippet  string
}

func (e *MountsError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "project %s: service %s does not have the mounts Baton needs: %s.\n", e.Project, e.Service, strings.Join(e.Problems, "; "))
	b.WriteString("Add this to your own Compose override file, one that git does not track and that is listed in files of [projects." + e.Project + ".compose] in the Baton config, after the Project's own files (Compose merges volumes by their path in the container and the later file wins, so a mount that is already there under another source or mode is replaced by it, not added to):\n\n")
	b.WriteString(e.Snippet)
	b.WriteString("\nAdding mounts needs one recreate of the container (docker compose")
	for _, f := range e.Files {
		b.WriteString(" -f " + f)
	}
	b.WriteString(" up -d " + e.Service + "), which ends every Session running in it, so run it when none is. Baton never edits Compose files and never recreates a container.")
	return b.String()
}

// snippet is the YAML for the override file.
func snippet(service string, mounts []want) string {
	var b strings.Builder
	b.WriteString("services:\n  " + service + ":\n    volumes:\n")
	for _, m := range mounts {
		b.WriteString("      - " + yamlScalar(m.volume()) + "\n")
	}
	return b.String()
}

// yamlScalar quotes a string for YAML unless it is made of characters that
// need no quoting in a list item.
func yamlScalar(s string) string {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("/._-:~+@%=,", r) {
			return strconv.Quote(s)
		}
	}
	return s
}
