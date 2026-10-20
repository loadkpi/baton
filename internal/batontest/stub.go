package batontest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// defaultStubs are the external programs of the outside world. Every Env
// installs a recording stub for each of them, so a test can never reach the
// real program even though the real ones exist on the machine.
var defaultStubs = []string{"guake", "docker", "notify-send", "claude", "codex"}

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

	log := e.callLog(name)
	// Record format: argument count, working directory, then each argument,
	// all NUL-terminated, so any argument value survives unchanged.
	body := fmt.Sprintf(`#!/bin/sh
{
  printf '%%s\0' "$#" "$(pwd)"
  for arg in "$@"; do printf '%%s\0' "$arg"; done
} >> %s
%s
`, shellQuote(log), spec.script)

	path := filepath.Join(e.stubDir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		e.t.Fatalf("write stub %s: %v", name, err)
	}
}

// Calls returns the recorded invocations of the stub called name, oldest first.
func (e *Env) Calls(name string) []Call {
	e.t.Helper()
	data, err := os.ReadFile(e.callLog(name))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		e.t.Fatalf("read calls of %s: %v", name, err)
	}

	fields := bytes.Split(data, []byte{0})
	fields = fields[:len(fields)-1] // the log ends with a terminator
	var calls []Call
	for len(fields) > 0 {
		n, err := strconv.Atoi(string(fields[0]))
		if err != nil || len(fields) < 2+n {
			e.t.Fatalf("corrupt call log for %s", name)
		}
		call := Call{Dir: string(fields[1])}
		for _, a := range fields[2 : 2+n] {
			call.Args = append(call.Args, string(a))
		}
		calls = append(calls, call)
		fields = fields[2+n:]
	}
	return calls
}

func (e *Env) callLog(name string) string {
	return filepath.Join(e.callsDir, name+".log")
}

func shellQuote(s string) string {
	return "'" + string(bytes.ReplaceAll([]byte(s), []byte("'"), []byte(`'\''`))) + "'"
}
