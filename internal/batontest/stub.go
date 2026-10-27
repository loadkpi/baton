package batontest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// defaultStubs are the external programs of the outside world. Every Env
// installs a recording stub for each of them, so a test can never reach the
// real program even though the real ones exist on the machine.
var defaultStubs = []string{"guake", "gdbus", "docker", "notify-send", "systemctl", "claude", "codex", "pw-play", "paplay"}

// Call is one recorded invocation of a stub executable.
type Call struct {
	Args []string // arguments, without the program name
	Dir  string   // working directory of the invocation
}

// StubOption customizes a stub installed with Env.Stub.
type StubOption func(*stubSpec)

type stubSpec struct {
	script string // extra shell code, run after the call is recorded
}

// WithScript adds shell code that runs after the call is recorded. The stub's
// exit status is the status of the script's last command; "$@" holds the
// arguments. Use it for output, exit codes and side effects:
//
//	env.Stub("docker", batontest.WithScript(`echo "container-1"`))
func WithScript(script string) StubOption {
	return func(s *stubSpec) { s.script = script }
}

// Stub installs an executable called name at the front of PATH. It records
// every invocation (see Calls) and then runs the optional script. Installing
// the same name again replaces the earlier stub but keeps its recorded calls.
func (e *Env) Stub(name string, opts ...StubOption) {
	e.t.Helper()
	var spec stubSpec
	for _, o := range opts {
		o(&spec)
	}

	dir := e.callDir(name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatalf("create call directory of %s: %v", name, err)
	}
	// Record protocol: every call is one file in the stub's call directory,
	// so a reader can never see a call that is half written and concurrent
	// stubs can never interleave their bytes. The stub first claims the next
	// sequence number by making a directory called "<n>.claim" (mkdir fails
	// when it exists, so the number is claimed atomically and the numbers give
	// the order of the calls), writes the call under a dot-name, and renames it
	// to "<n>" as the very last step. The reader lists only the names that are
	// plain numbers. The record is argument count, working directory, then each
	// argument, all NUL-terminated, so any argument value survives unchanged.
	body := fmt.Sprintf(`#!/bin/sh
(
  dir=%s
  tmp="$dir/.$$"
  {
    printf '%%s\0' "$#" "$(pwd)"
    for arg in "$@"; do printf '%%s\0' "$arg"; done
  } > "$tmp"
  set -- "$dir"/*.claim
  [ -e "$1" ] && n=$# || n=0
  n=$((n + 1))
  while ! mkdir "$dir/$n.claim" 2>/dev/null; do n=$((n + 1)); done
  mv "$tmp" "$dir/$n"
)
%s
`, shellQuote(dir), spec.script)

	path := filepath.Join(e.stubDir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		e.t.Fatalf("write stub %s: %v", name, err)
	}
}

// Without makes the programs called names missing from the Env's PATH: their
// stubs are removed, and so are the real ones, which are on the system
// directories of PATH and would otherwise be run instead. From then on PATH
// holds the stub directory and a directory of links to the other programs of
// the system. Recorded calls stay.
func (e *Env) Without(names ...string) {
	e.t.Helper()
	hidden := make(map[string]bool, len(names))
	for _, name := range names {
		hidden[name] = true
		if err := os.Remove(filepath.Join(e.stubDir, name)); err != nil && !os.IsNotExist(err) {
			e.t.Fatalf("remove stub %s: %v", name, err)
		}
	}
	dir := filepath.Join(filepath.Dir(e.stubDir), "sysbin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	for _, sys := range e.sysDirs {
		entries, err := os.ReadDir(sys)
		if err != nil {
			e.t.Fatalf("read %s: %v", sys, err)
		}
		for _, entry := range entries {
			if hidden[entry.Name()] {
				continue
			}
			// The first directory of PATH wins, as it does when PATH is walked.
			if err := os.Symlink(filepath.Join(sys, entry.Name()), filepath.Join(dir, entry.Name())); err != nil && !os.IsExist(err) {
				e.t.Fatalf("link %s: %v", entry.Name(), err)
			}
		}
	}
	e.sysDirs = []string{dir}
}

// Calls returns the recorded invocations of the stub called name, oldest first.
// It reads only complete records (see Stub), so it is safe to call while a
// process that runs a stub is still going.
func (e *Env) Calls(name string) []Call {
	e.t.Helper()
	dir := e.callDir(name)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		e.t.Fatalf("read calls of %s: %v", name, err)
	}

	var numbers []int // the sequence numbers of the complete records
	for _, entry := range entries {
		// Claims and calls that are still being recorded have other names.
		if n, err := strconv.Atoi(entry.Name()); err == nil && n > 0 {
			numbers = append(numbers, n)
		}
	}
	sort.Ints(numbers)

	var calls []Call
	for _, number := range numbers {
		file := strconv.Itoa(number)
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			e.t.Fatalf("read call %s of %s: %v", file, name, err)
		}
		fields := bytes.Split(data, []byte{0})
		fields = fields[:len(fields)-1] // the record ends with a terminator
		n := -1
		if len(fields) >= 2 {
			n, err = strconv.Atoi(string(fields[0]))
		}
		if err != nil || n < 0 || len(fields) != 2+n {
			e.t.Fatalf("corrupt call record %s for %s", file, name)
		}
		call := Call{Dir: string(fields[1])}
		for _, a := range fields[2:] {
			call.Args = append(call.Args, string(a))
		}
		calls = append(calls, call)
	}
	return calls
}

func (e *Env) callDir(name string) string {
	return filepath.Join(e.callsDir, name)
}

func shellQuote(s string) string {
	return "'" + string(bytes.ReplaceAll([]byte(s), []byte("'"), []byte(`'\''`))) + "'"
}

// The guake stub plays what Guake does to the command it is given with -e: it
// types the line into the shell of a new tab, which starts the program a moment
// later. The stub starts processes in the background, detached (own session,
// stdin and output from /dev/null, so that no pipe of the test stays open), and
// every one of them is killed when the test ends. A process of the stub is the
// real thing for Baton, which looks for it in /proc.
//
// A bare `env.Stub("guake")` records the call and starts nothing, the tab in
// which the line never ran. Every Env starts with GuakeStarts of the programs
// the Runtimes launch (claude, codex and docker), so that a launch is found
// unless a test says otherwise.

// GuakeStarts makes the guake stub start a process called each of names, in
// the directory the -e line changes into, for every call with -e. Each process
// is a copy of sleep(1) under that name, so that its name in /proc is the name.
func (e *Env) GuakeStarts(names ...string) StubOption {
	return WithScript(e.guakeScript(`dir=$(sh -c "${line%% && *} && pwd -P")`, names))
}

// GuakeStartsIn is GuakeStarts in a fixed directory instead of the one the line
// changes into: a program that has the right name but is not in the tab.
func (e *Env) GuakeStartsIn(dir string, names ...string) StubOption {
	return WithScript(e.guakeScript("dir="+shellQuote(dir), names))
}

// GuakeRunsLine makes the guake stub run the -e line in a shell, as the shell
// of a tab would, so that the programs on the PATH of the Env (the stubs of
// claude, codex and docker) run as real processes in the directory the line
// changes into. A stub that has to stay alive long enough to be found says
// so itself, for example batontest.WithScript("sleep 30").
func (e *Env) GuakeRunsLine() StubOption {
	return WithScript(e.guakeScript("", nil))
}

func (e *Env) guakeScript(dir string, names []string) string {
	procs := shellQuote(e.procsDir)
	var b strings.Builder
	fmt.Fprintf(&b, `procs=%s
mkdir -p "$procs/bin"
line=
while [ "$#" -gt 0 ]; do
  [ "$1" = -e ] && line=$2
  shift
done
[ -n "$line" ] || exit 0
`, procs)
	if len(names) == 0 {
		b.WriteString(`(setsid sh -c "$line" </dev/null >/dev/null 2>&1 & echo $! >> "$procs/pids")
`)
		return b.String()
	}
	b.WriteString(dir + "\n")
	fmt.Fprintf(&b, `for name in %s; do
  if [ ! -x "$procs/bin/$name" ]; then
    cp "$(command -v sleep)" "$procs/bin/.$name.$$" && mv "$procs/bin/.$name.$$" "$procs/bin/$name"
  fi
  (cd "$dir" && setsid "$procs/bin/$name" 60 </dev/null >/dev/null 2>&1 & echo $! >> "$procs/pids")
done
`, strings.Join(names, " "))
	return b.String()
}

// killStubProcesses kills the processes (and their process groups) that the
// guake stub started.
func (e *Env) killStubProcesses() {
	data, err := os.ReadFile(filepath.Join(e.procsDir, "pids"))
	if err != nil {
		return
	}
	for _, field := range strings.Fields(string(data)) {
		if pid, err := strconv.Atoi(field); err == nil && pid > 1 {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

// GdbusTabs makes the gdbus stub answer for Guake's D-Bus interface with tabs
// that have the given titles, in order: get_tab_count with `(N,)`,
// get_tab_name <i> with `('title',)`, and nothing for the rest (select_tab,
// show). Every call is recorded: the arguments of `select_tab 1` are
// "call --session --dest ... --method org.guake3.RemoteControl.select_tab 1".
func (e *Env) GdbusTabs(titles ...string) StubOption {
	var b strings.Builder
	b.WriteString(`method=; param=
while [ "$#" -gt 0 ]; do
  if [ "$1" = --method ]; then method=${2##*.}; param=$3; fi
  shift
done
case "$method" in
`)
	fmt.Fprintf(&b, "get_tab_count) echo '(%d,)';;\nget_tab_name) case \"$param\" in\n", len(titles))
	for i, title := range titles {
		fmt.Fprintf(&b, "%d) printf '%%s\\n' %s;;\n", i, shellQuote("('"+title+"',)"))
	}
	b.WriteString("esac;;\nesac\n")
	return WithScript(b.String())
}
