package batontest

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Env is one isolated world for one test: its own HOME, a source repository,
// and a PATH that starts with recording stubs of the external programs.
type Env struct {
	t   *testing.T
	bin string

	// Home is the HOME that baton runs with. Nothing outside it (and the
	// other directories of this Env) is ever touched.
	Home string
	// SourceDir is the temporary git repository that plays the user's private
	// source repository.
	SourceDir string

	stubDir  string
	callsDir string
}

// New creates an Env around the baton binary built by BuildBinary. It makes
// the isolated HOME, initializes the source git repository and installs the
// default stubs.
func New(t *testing.T, bin string) *Env {
	t.Helper()
	root := t.TempDir()
	e := &Env{
		t:         t,
		bin:       bin,
		Home:      filepath.Join(root, "home"),
		SourceDir: filepath.Join(root, "source"),
		stubDir:   filepath.Join(root, "stubs"),
		callsDir:  filepath.Join(root, "calls"),
	}
	for _, d := range []string{e.Home, e.SourceDir, e.stubDir, e.callsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.git(e.SourceDir, "init", "--quiet", "--initial-branch=main")
	for _, name := range defaultStubs {
		e.Stub(name)
	}
	return e
}

// Result is the outcome of one process run by an Env.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Run runs baton with args, in HOME, and returns its output and exit code.
// A non-zero exit code is not a test failure.
func (e *Env) Run(args ...string) Result {
	e.t.Helper()
	return e.run("", e.bin, args...)
}

// RunWithStdin is Run with the given text on standard input.
func (e *Env) RunWithStdin(stdin string, args ...string) Result {
	e.t.Helper()
	return e.run(stdin, e.bin, args...)
}

// Command runs any program from the Env's PATH (a stub, git, ...) in HOME,
// with the same isolated environment baton gets.
func (e *Env) Command(name string, args ...string) Result {
	e.t.Helper()
	return e.run("", name, args...)
}

func (e *Env) run(stdin, name string, args ...string) Result {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, e.lookPath(name), args...)
	cmd.Dir = e.Home
	cmd.Env = e.Environ()
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		e.t.Fatalf("run %s %v: %v", name, args, err)
	}
	return res
}

// lookPath resolves a program name against the Env's PATH. exec.Command would
// resolve it against the PATH of the test process, which would run the real
// guake or docker instead of the stub.
func (e *Env) lookPath(name string) string {
	if strings.Contains(name, "/") {
		return name
	}
	for _, dir := range e.pathDirs() {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	e.t.Fatalf("%s not found on the Env's PATH %v", name, e.pathDirs())
	return ""
}

func (e *Env) pathDirs() []string {
	return []string{e.stubDir, "/usr/bin", "/bin"}
}

// Environ is the complete environment of every process an Env runs. It is
// built from scratch: nothing is inherited from the test process, so API keys,
// XDG variables and git configuration of the developer never leak in.
func (e *Env) Environ() []string {
	return []string{
		"HOME=" + e.Home,
		"PATH=" + strings.Join(e.pathDirs(), ":"),
		"LANG=C",
		"TERM=dumb",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Baton Test",
		"GIT_AUTHOR_EMAIL=test@baton.invalid",
		"GIT_COMMITTER_NAME=Baton Test",
		"GIT_COMMITTER_EMAIL=test@baton.invalid",
	}
}

// Path resolves a path relative to HOME.
func (e *Env) Path(elem ...string) string {
	return filepath.Join(append([]string{e.Home}, elem...)...)
}

// WriteFile creates a file (and its directories) at path with mode 0644, or
// 0755 when exec is true. The path is absolute or relative to HOME.
func (e *Env) WriteFile(path, content string, executable ...bool) {
	e.t.Helper()
	if !filepath.IsAbs(path) {
		path = e.Path(path)
	}
	mode := os.FileMode(0o644)
	if len(executable) > 0 && executable[0] {
		mode = 0o755
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		e.t.Fatal(err)
	}
	// WriteFile does not change the mode of an existing file.
	if err := os.Chmod(path, mode); err != nil {
		e.t.Fatal(err)
	}
}

// ReadFile returns the content of a file; the path is absolute or relative to
// HOME. A missing file fails the test.
func (e *Env) ReadFile(path string) string {
	e.t.Helper()
	if !filepath.IsAbs(path) {
		path = e.Path(path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(data)
}

// Exists reports whether a path (absolute or relative to HOME) exists.
func (e *Env) Exists(path string) bool {
	e.t.Helper()
	if !filepath.IsAbs(path) {
		path = e.Path(path)
	}
	_, err := os.Lstat(path)
	return err == nil
}

// SourceFile writes a file into the source repository and returns nothing;
// rel is relative to SourceDir. Call CommitSource to record it in git.
func (e *Env) SourceFile(rel, content string, executable ...bool) {
	e.t.Helper()
	e.WriteFile(filepath.Join(e.SourceDir, rel), content, executable...)
}

// CommitSource commits everything in the source repository.
func (e *Env) CommitSource(message string) {
	e.t.Helper()
	e.git(e.SourceDir, "add", "--all")
	e.git(e.SourceDir, "commit", "--quiet", "--allow-empty", "-m", message)
}

func (e *Env) git(dir string, args ...string) {
	e.t.Helper()
	cmd := exec.Command(e.lookPath("git"), args...)
	cmd.Dir = dir
	cmd.Env = e.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// ConfigPath is where baton reads its configuration in the isolated HOME.
func (e *Env) ConfigPath() string {
	return e.Path(".config", "baton", "config.toml")
}

// WriteConfig writes the Baton config file.
func (e *Env) WriteConfig(content string) {
	e.t.Helper()
	e.WriteFile(e.ConfigPath(), content)
}

// MakeProfileDir creates an empty Profile directory under HOME (for example
// ".claude-personal") and returns its absolute path.
func (e *Env) MakeProfileDir(name string) string {
	e.t.Helper()
	dir := e.Path(name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	return dir
}

// Mode returns the permission bits of a file; the path is absolute or relative
// to HOME.
func (e *Env) Mode(path string) os.FileMode {
	e.t.Helper()
	if !filepath.IsAbs(path) {
		path = e.Path(path)
	}
	info, err := os.Stat(path)
	if err != nil {
		e.t.Fatal(err)
	}
	return info.Mode().Perm()
}

// ModTime returns the modification time of a file; the path is absolute or
// relative to HOME. Tests use it to prove that a second run rewrote nothing.
func (e *Env) ModTime(path string) time.Time {
	e.t.Helper()
	if !filepath.IsAbs(path) {
		path = e.Path(path)
	}
	info, err := os.Stat(path)
	if err != nil {
		e.t.Fatal(err)
	}
	return info.ModTime()
}
