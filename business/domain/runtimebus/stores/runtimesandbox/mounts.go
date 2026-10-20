package runtimesandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/foundation/atomicfile"
)

// mount is one bind mount of a container: a host path seen at a path inside.
type mount struct {
	Host      string
	Container string
	ReadOnly  bool
}

// spec is the mount as `docker run -v` takes it.
func (m mount) spec() string {
	mode := "rw"
	if m.ReadOnly {
		mode = "ro"
	}
	return m.Host + ":" + m.Container + ":" + mode
}

// mountsOf returns the bind mounts among the options of a `docker run`
// (`-v src:dst[:options]`, also as --volume), in order. Anything else that
// ai-sandbox passes, such as tmpfs, is not a path of the host and is ignored.
func mountsOf(options []string) []mount {
	var out []mount
	for i := 0; i+1 < len(options); i++ {
		if options[i] != "-v" && options[i] != "--volume" {
			continue
		}
		if m, ok := parseMount(options[i+1]); ok {
			out = append(out, m)
		}
	}
	return out
}

func parseMount(spec string) (mount, bool) {
	parts := strings.Split(spec, ":")
	if len(parts) < 2 || len(parts) > 3 || !path.IsAbs(parts[0]) || !path.IsAbs(parts[1]) {
		return mount{}, false // a named volume, or a form this does not know
	}
	m := mount{Host: parts[0], Container: path.Clean(parts[1])}
	if len(parts) == 3 {
		m.ReadOnly = strings.Contains(","+parts[2]+",", ",ro,")
	}
	return m, true
}

// fileMounts is the document of a mounts record. Primitive types only.
type fileMounts struct {
	V      int         `json:"v"`
	Mounts []fileMount `json:"mounts"`
}

type fileMount struct {
	Host      string `json:"host"`
	Container string `json:"container"`
	ReadOnly  bool   `json:"readonly,omitempty"`
}

func toFileMount(m mount) fileMount { return fileMount(m) }

func toBusMount(m fileMount) mount { return mount(m) }

// mountsPath is the record of the mounts of a Project's container. It is not in
// the Project's Baton directory, which the container can write.
func (r *Runtime) mountsPath(project projectname.Name) string {
	return filepath.Join(r.batonDir, "mounts", project.String()+".json")
}

// saveMounts records the mounts the last container of the Project was started
// with, so that paths seen from inside can be mapped back to the host.
func (r *Runtime) saveMounts(project projectname.Name, mounts []mount) error {
	doc := fileMounts{V: 1}
	for _, m := range mounts {
		doc.Mounts = append(doc.Mounts, toFileMount(m))
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.mountsPath(project)), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(r.mountsPath(project), append(data, '\n'), 0o600)
}

func (r *Runtime) queryMounts(project projectname.Name) ([]mount, error) {
	data, err := os.ReadFile(r.mountsPath(project))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("no container of project %s was started by baton, so its mounts are not known", project)
	}
	if err != nil {
		return nil, err
	}
	var doc fileMounts
	if err := json.Unmarshal(data, &doc); err != nil || doc.V != 1 {
		return nil, fmt.Errorf("%s is not a mounts record", r.mountsPath(project))
	}
	mounts := make([]mount, 0, len(doc.Mounts))
	for _, m := range doc.Mounts {
		mounts = append(mounts, toBusMount(m))
	}
	return mounts, nil
}

// HostPath maps a path seen inside the Project's container to the host path of
// the mount that holds it: the longest mount whose container path is the path
// or a parent of it. The mounts are those Start passed to docker the last time
// it started the Project's container; nothing assumes the two sides are named
// alike. A path no mount holds (the container's own /tmp, say) has no host
// path.
func (r *Runtime) HostPath(_ context.Context, project projectname.Name, p string) (string, error) {
	if !path.IsAbs(p) {
		return "", fmt.Errorf("%q is not an absolute path", p)
	}
	mounts, err := r.queryMounts(project)
	if err != nil {
		return "", err
	}
	p = path.Clean(p)

	var best *mount
	for i := range mounts {
		m := &mounts[i]
		if (p == m.Container || strings.HasPrefix(p, strings.TrimSuffix(m.Container, "/")+"/")) &&
			(best == nil || len(m.Container) >= len(best.Container)) {
			best = m
		}
	}
	if best == nil {
		return "", fmt.Errorf("%s is not under a mount of the container of project %s", p, project)
	}
	return filepath.Join(best.Host, filepath.FromSlash(strings.TrimPrefix(p, best.Container))), nil
}
