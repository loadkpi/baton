// Package batontest is the test harness for the single test seam of Baton:
// the built baton executable run as a subprocess against an isolated HOME.
//
// A test package builds the binary once in TestMain with BuildBinary and
// creates one Env per test with New. Everything an Env creates lives in
// t.TempDir(), and baton never sees the real home directory or environment.
package batontest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// BuildBinary compiles ./app/baton of the module that contains the working
// directory into a fresh temporary directory (CGO disabled, like the release
// binary). It returns the binary path and a cleanup function. Call it once per
// test run, from TestMain. The fake docker of the Compose tests
// (internal/batontest/dockerfake) is built next to it.
func BuildBinary() (path string, cleanup func(), err error) {
	root, err := moduleRoot()
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "baton-bin-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	path = filepath.Join(dir, "baton")
	for _, b := range []struct{ out, pkg string }{{path, "./app/baton"}, {fakeDockerPath(path), "./internal/batontest/dockerfake"}} {
		cmd := exec.Command("go", "build", "-o", b.out, b.pkg)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("go build %s: %w\n%s", b.pkg, err, out)
		}
	}
	return path, cleanup, nil
}

// moduleRoot walks up from the working directory to the directory with go.mod.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above the working directory")
		}
		dir = parent
	}
}

// fakeDockerPath is where BuildBinary puts the fake docker, given the baton
// binary.
func fakeDockerPath(bin string) string {
	return filepath.Join(filepath.Dir(bin), "dockerfake")
}
