// Package git is a thin wrapper around the git executable.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Tracked returns those of the absolute paths that git tracks: a file that is
// in the index of the repository around it, or a directory that holds such a
// file. A path that does not exist yet is checked as well (the index can name
// a file that is missing from the work tree). Paths outside any repository are
// not tracked. The result keeps the order of paths.
func Tracked(ctx context.Context, paths []string) ([]string, error) {
	// One git call answers for all paths that share the nearest existing
	// directory; that is usually one call per Profile.
	groups := make(map[string][]string)
	var order []string
	for _, p := range paths {
		dir := nearestDir(p)
		if _, ok := groups[dir]; !ok {
			order = append(order, dir)
		}
		groups[dir] = append(groups[dir], p)
	}

	tracked := make(map[string]bool)
	for _, dir := range order {
		names, ok, err := lsFiles(ctx, dir, groups[dir])
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		for _, p := range groups[dir] {
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return nil, err
			}
			for _, name := range names {
				if rel == "." || name == rel || strings.HasPrefix(name, rel+"/") {
					tracked[p] = true
					break
				}
			}
		}
	}

	var out []string
	for _, p := range paths {
		if tracked[p] {
			out = append(out, p)
		}
	}
	return out, nil
}

// nearestDir is the closest existing directory above p (p itself when it is
// one).
func nearestDir(p string) string {
	for {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}

// lsFiles lists the tracked files below dir that match the paths, relative to
// dir. ok is false when dir is not inside a repository.
func lsFiles(ctx context.Context, dir string, paths []string) (names []string, ok bool, err error) {
	args := []string{"-C", dir, "ls-files", "-z", "--"}
	for _, p := range paths {
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil, false, err
		}
		if rel != "." {
			rel = ":(literal)" + filepath.ToSlash(rel)
		}
		args = append(args, rel)
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = cleanEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		switch {
		case errors.Is(err, exec.ErrNotFound):
			return nil, false, fmt.Errorf("git is needed to check whether %s is tracked: %w", dir, err)
		case strings.Contains(stderr.String(), "not a git repository"):
			return nil, false, nil
		default:
			return nil, false, fmt.Errorf("git ls-files in %s: %w: %s", dir, err, strings.TrimSpace(stderr.String()))
		}
	}

	for _, name := range strings.Split(stdout.String(), "\x00") {
		if name != "" {
			names = append(names, name)
		}
	}
	return names, true, nil
}

// cleanEnv drops the variables that would point git at another repository than
// the one around the directory.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		switch name, _, _ := strings.Cut(kv, "="); name {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_PREFIX":
			continue
		}
		env = append(env, kv)
	}
	return env
}

// Repo is where a directory sits in a git repository.
type Repo struct {
	// Root is the root of the work tree that contains the directory: the main
	// checkout, or a linked worktree.
	Root string
	// Main is the root of the main checkout of the repository. It equals Root
	// for the main checkout itself, and for a repository whose common git
	// directory is not <root>/.git (a submodule, say), where no main checkout
	// can be derived.
	Main string
}

// Locate finds the work tree that contains dir and the main checkout of its
// repository. A linked worktree (`git worktree add`) has its own Root but
// shares the common git directory, and with it Main, with the checkout it was
// made from. ok is false when dir is not inside a work tree (outside git, in a
// bare repository, or in a .git directory), or does not exist.
func Locate(ctx context.Context, dir string) (repo Repo, ok bool, err error) {
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
		return Repo{}, false, nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
	cmd.Env = cleanEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		switch {
		case errors.Is(err, exec.ErrNotFound):
			return Repo{}, false, fmt.Errorf("git is needed to find the Project of %s: %w", dir, err)
		case strings.Contains(stderr.String(), "not a git repository"),
			strings.Contains(stderr.String(), "must be run in a work tree"):
			return Repo{}, false, nil
		default:
			return Repo{}, false, fmt.Errorf("git rev-parse in %s: %w: %s", dir, err, strings.TrimSpace(stderr.String()))
		}
	}

	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 2 {
		return Repo{}, false, fmt.Errorf("git rev-parse in %s: unexpected output %q", dir, stdout.String())
	}
	repo = Repo{Root: lines[0], Main: lines[0]}
	if common := lines[1]; filepath.Base(common) == ".git" {
		repo.Main = filepath.Dir(common)
	}
	return repo, true, nil
}
