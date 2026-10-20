package batontest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

// defaultStubs are the external programs of the outside world. Every Env
// installs a recording stub for each of them, so a test can never reach the
// real program even though the real ones exist on the machine.
var defaultStubs = []string{"guake", "docker", "notify-send", "systemctl", "claude", "codex"}

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
