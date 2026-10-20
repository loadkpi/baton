package runtimecompose

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/loadkpi/baton/foundation/docker"
)

// hostPath maps a path inside the container to the host path it stands for:
// the longest bind mount whose destination holds it decides, and the rest of
// the path is appended to the mount's source. Volumes have no host path.
func hostPath(mounts []docker.Mount, p string) (string, error) {
	if !path.IsAbs(p) {
		return "", fmt.Errorf("%q is not an absolute path", p)
	}
	p = path.Clean(p)
	best := -1
	for i, m := range mounts {
		if m.Type != "bind" || !within(m.Destination, p) {
			continue
		}
		if best < 0 || len(m.Destination) > len(mounts[best].Destination) {
			best = i
		}
	}
	if best < 0 {
		return "", fmt.Errorf("%s is under no bind mount of the container", p)
	}
	m := mounts[best]
	return filepath.Join(m.Source, strings.TrimPrefix(p, path.Clean(m.Destination))), nil
}

// containerPath is the inverse of hostPath: the path inside the container of
// a host path, by the bind mount with the longest source that holds it. A
// source is compared as the real path it is, because git resolves the links
// of a Working copy and Compose does not.
func containerPath(mounts []docker.Mount, p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%q is not an absolute path", p)
	}
	p = realPath(p)
	best, bestSource := -1, ""
	for i, m := range mounts {
		if m.Type != "bind" {
			continue
		}
		source := realPath(m.Source)
		if !within(source, p) {
			continue
		}
		if best < 0 || len(source) > len(bestSource) {
			best, bestSource = i, source
		}
	}
	if best < 0 {
		return "", fmt.Errorf("%s is under no bind mount of the container", p)
	}
	return path.Join(mounts[best].Destination, strings.TrimPrefix(p, bestSource)), nil
}

// within is true when p is dir or a path under it.
func within(dir, p string) bool {
	dir = strings.TrimSuffix(dir, "/")
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// realPath is p with its links resolved, or cleaned when it does not exist (or
// is not a path of this machine).
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
