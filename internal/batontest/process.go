package batontest

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// processTimeout bounds every wait on a started process.
const processTimeout = 15 * time.Second

// Process is a baton process that keeps running while the test works, such as
// `baton watch`. It is killed when the test ends, so a failing test leaves
// nothing behind.
type Process struct {
	t   *testing.T
	cmd *exec.Cmd

	mu     sync.Mutex
	stdout lockedBuffer
	stderr lockedBuffer

	done chan struct{}
	err  error
}

// lockedBuffer is a bytes.Buffer that the process writes and the test reads.
type lockedBuffer struct {
	mu  *sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Start starts baton with args, in HOME, with the Env's isolated environment,
// without waiting for it. The process is killed in t.Cleanup.
func (e *Env) Start(args ...string) *Process {
	e.t.Helper()
	p := &Process{t: e.t, done: make(chan struct{})}
	p.stdout.mu, p.stderr.mu = &p.mu, &p.mu

	p.cmd = exec.Command(e.bin, args...)
	p.cmd.Dir = e.Home
	p.cmd.Env = e.Environ()
	p.cmd.Stdout, p.cmd.Stderr = &p.stdout, &p.stderr
	if err := p.cmd.Start(); err != nil {
		e.t.Fatalf("start baton %v: %v", args, err)
	}
	go func() {
		p.err = p.cmd.Wait()
		close(p.done)
	}()
	e.t.Cleanup(func() {
		_ = p.cmd.Process.Kill()
		<-p.done
	})
	return p
}

// Stdout is what the process has written to its standard output so far.
func (p *Process) Stdout() string { return p.stdout.String() }

// Stderr is what the process has written to its standard error so far.
func (p *Process) Stderr() string { return p.stderr.String() }

// WaitStdout waits until the standard output holds want and fails the test if
// that takes too long or the process ends first.
func (p *Process) WaitStdout(want string) { p.t.Helper(); p.wait("stdout", want, p.Stdout) }

// WaitStderr is WaitStdout for the standard error.
func (p *Process) WaitStderr(want string) { p.t.Helper(); p.wait("stderr", want, p.Stderr) }

func (p *Process) wait(name, want string, read func() string) {
	p.t.Helper()
	deadline := time.Now().Add(processTimeout)
	for !strings.Contains(read(), want) {
		select {
		case <-p.done:
			p.t.Fatalf("baton ended without writing %q to %s; stdout=%q stderr=%q", want, name, p.Stdout(), p.Stderr())
		default:
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("timed out waiting for %q on %s; stdout=%q stderr=%q", want, name, p.Stdout(), p.Stderr())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Stop asks the process to end with SIGTERM, the way systemd does, waits for
// it and returns what it wrote and its exit code (-1 when a signal ended it).
func (p *Process) Stop() Result {
	p.t.Helper()
	return p.Signal(syscall.SIGTERM)
}

// Interrupt sends SIGINT, the Ctrl-C of a terminal, and waits like Stop.
func (p *Process) Interrupt() Result {
	p.t.Helper()
	return p.Signal(syscall.SIGINT)
}

// Signal sends sig and waits for the process to end like Stop.
func (p *Process) Signal(sig syscall.Signal) Result {
	p.t.Helper()
	_ = p.cmd.Process.Signal(sig)
	return p.Wait()
}

// Wait waits, without signalling, for the process to end on its own and
// returns what it wrote and its exit code (-1 when a signal ended it).
func (p *Process) Wait() Result {
	p.t.Helper()
	select {
	case <-p.done:
	case <-time.After(processTimeout):
		p.t.Fatalf("baton did not end; stdout=%q stderr=%q", p.Stdout(), p.Stderr())
	}
	res := Result{Stdout: p.Stdout(), Stderr: p.Stderr()}
	var exitErr *exec.ExitError
	switch {
	case p.err == nil:
	case errors.As(p.err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		p.t.Fatalf("wait for baton: %v", p.err)
	}
	return res
}
