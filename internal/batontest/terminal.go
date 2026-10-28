package batontest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// terminalTimeout bounds every wait on a Terminal, so a baton that asks a
// question the test did not expect fails the test instead of hanging it.
const terminalTimeout = 15 * time.Second

// Terminal is a baton process whose standard input, output and error are a
// pseudo-terminal, so baton sees a real terminal and can ask questions. The
// test plays the user: it waits for text with Expect and types with Send.
//
// The pseudo-terminal is made with the standard library only (Linux, like the
// rest of Baton), so tests need no third-party pty package.
type Terminal struct {
	t      *testing.T
	cmd    *exec.Cmd
	master *os.File
	cancel context.CancelFunc

	mu       sync.Mutex
	cond     *sync.Cond
	output   strings.Builder // everything the process wrote, "\r\n" turned into "\n"
	readTo   int             // Expect has consumed output up to here
	finished bool            // the master side reached end of file
	done     chan struct{}   // closed when pump returns
}

// StartTerminal starts baton with args on a pseudo-terminal, in HOME, with the
// Env's isolated environment. The test must call Wait.
func (e *Env) StartTerminal(args ...string) *Terminal {
	e.t.Helper()
	return e.StartTerminalSized(0, args...)
}

// StartTerminalSized is StartTerminal on a terminal cols characters wide; 0
// leaves the size unset, as StartTerminal does.
func (e *Env) StartTerminalSized(cols int, args ...string) *Terminal {
	e.t.Helper()
	master, slave, err := openPTY()
	if err != nil {
		e.t.Fatalf("open pseudo-terminal: %v", err)
	}
	if cols > 0 {
		ws := struct{ Row, Col, X, Y uint16 }{Row: 40, Col: uint16(cols)}
		if err := ioctl(master, syscall.TIOCSWINSZ, unsafe.Pointer(&ws)); err != nil {
			e.t.Fatalf("set the terminal size: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cmd := exec.CommandContext(ctx, e.lookPath(e.bin), args...)
	cmd.Dir = e.Home
	cmd.Env = e.Environ()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		cancel()
		e.t.Fatalf("start %s %v: %v", e.bin, args, err)
	}
	_ = slave.Close() // the child holds its own copy

	term := &Terminal{t: e.t, cmd: cmd, master: master, cancel: cancel, done: make(chan struct{})}
	term.cond = sync.NewCond(&term.mu)
	go term.pump()
	return term
}

// RunTerminal runs baton on a pseudo-terminal and types the replies, one for
// each of the prompts that end in "[y/N] ", in order. It returns the whole
// transcript as Stdout. Use StartTerminal for anything less regular.
func (e *Env) RunTerminal(replies []string, args ...string) Result {
	e.t.Helper()
	term := e.StartTerminal(args...)
	for _, r := range replies {
		term.Expect("[y/N] ")
		term.Send(r + "\n")
	}
	return term.Wait()
}

// pump copies what the process writes into the transcript until the terminal
// is closed (EIO on Linux once the last holder of the slave side is gone).
func (term *Terminal) pump() {
	defer close(term.done)
	buf := make([]byte, 4096)
	for {
		n, err := term.master.Read(buf)
		term.mu.Lock()
		if n > 0 {
			term.output.WriteString(strings.ReplaceAll(string(buf[:n]), "\r\n", "\n"))
		}
		if err != nil {
			term.finished = true
		}
		term.cond.Broadcast()
		term.mu.Unlock()
		if err != nil {
			return
		}
	}
}

// Expect waits until the process has written want (after the text consumed by
// the previous Expect) and fails the test if that takes too long.
func (term *Terminal) Expect(want string) {
	term.t.Helper()
	deadline := time.Now().Add(terminalTimeout)
	timer := time.AfterFunc(terminalTimeout, func() {
		term.mu.Lock()
		defer term.mu.Unlock()
		term.cond.Broadcast()
	})
	defer timer.Stop()

	term.mu.Lock()
	defer term.mu.Unlock()
	for {
		out := term.output.String()
		if i := strings.Index(out[term.readTo:], want); i >= 0 {
			term.readTo += i + len(want)
			return
		}
		if term.finished {
			term.t.Fatalf("baton ended without writing %q; transcript so far:\n%s", want, out)
		}
		if time.Now().After(deadline) {
			term.t.Fatalf("timed out waiting for %q; transcript so far:\n%s", want, out)
		}
		term.cond.Wait()
	}
}

// Send types text, as the user would (end a line with "\n").
func (term *Terminal) Send(text string) {
	term.t.Helper()
	if _, err := term.master.WriteString(text); err != nil {
		term.t.Fatalf("type into the terminal: %v", err)
	}
}

// Wait waits for baton to exit and returns the transcript (Stdout; a terminal
// has no separate error stream) and the exit code.
func (term *Terminal) Wait() Result {
	term.t.Helper()
	defer term.cancel()

	err := term.cmd.Wait()
	// The process is gone; the pump ends once the master has drained.
	select {
	case <-term.done:
	case <-time.After(terminalTimeout):
		term.t.Fatal("the terminal output did not end after baton exited")
	}
	term.mu.Lock()
	out := term.output.String()
	term.mu.Unlock()
	_ = term.master.Close()

	res := Result{Stdout: out}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		term.t.Fatalf("wait for baton: %v", err)
	}
	return res
}

// openPTY returns the two ends of a new pseudo-terminal (Linux).
func openPTY() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (*os.File, *os.File, error) {
		_ = master.Close()
		return nil, nil, err
	}

	var unlock int32 // 0 unlocks the slave side
	if err := ioctl(master, syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)); err != nil {
		return fail(fmt.Errorf("unlock: %w", err))
	}
	var number uint32
	if err := ioctl(master, syscall.TIOCGPTN, unsafe.Pointer(&number)); err != nil {
		return fail(fmt.Errorf("slave number: %w", err))
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return fail(err)
	}
	return master, slave, nil
}

func ioctl(f *os.File, request uintptr, arg unsafe.Pointer) error {
	conn, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var errno syscall.Errno
	if err := conn.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(arg))
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}
