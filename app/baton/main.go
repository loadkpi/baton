// Command baton is the single binary of Baton: it wires the commands to the
// business layer. Only primitive types live here.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// console is where a command writes. Write errors on a closed terminal or pipe
// are not actionable, so the helpers ignore them.
type console struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func (c console) println(a ...any) {
	_, _ = fmt.Fprintln(c.stdout, a...)
}

// errorf prints "baton: <message>" to stderr and returns the exit code for a
// failed command.
func (c console) errorf(format string, a ...any) int {
	_, _ = fmt.Fprintf(c.stderr, "baton: "+format+"\n", a...)
	return exitError
}

func (c console) warnf(format string, a ...any) {
	_, _ = fmt.Fprintf(c.stderr, "baton: warning: "+format+"\n", a...)
}

func (c console) usagef(format string, a ...any) int {
	_, _ = fmt.Fprintf(c.stderr, "baton: "+format+"\n", a...)
	return exitUsage
}

// run executes one command line and returns the process exit code.
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	c := console{stdin: stdin, stdout: stdout, stderr: stderr}
	if len(args) == 0 {
		return c.usagef("a command is required (available: sync, new, hook, ls)")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return c.errorf("%v", err)
	}

	switch args[0] {
	case "sync":
		return runSync(ctx, c, home)
	case "new":
		return runNew(ctx, c, home, args[1:])
	case "hook":
		return runHook(ctx, c, home, args[1:])
	case "ls":
		return runLs(ctx, c, home, args[1:])
	default:
		return c.usagef("unknown command %q (available: sync, new, hook, ls)", args[0])
	}
}
