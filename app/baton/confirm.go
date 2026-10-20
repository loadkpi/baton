package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/loadkpi/baton/business/domain/syncbus"
	"github.com/loadkpi/baton/foundation/terminal"
)

// confirmer asks the user questions on the terminal. The question goes to
// standard error, so standard output keeps only results; the answer is read
// from standard input. There is a terminal to ask in only when both are one.
type confirmer struct {
	out         io.Writer
	in          *bufio.Reader
	interactive bool
}

func newConfirmer(c console) *confirmer {
	stdin, _ := c.stdin.(*os.File)
	stderr, _ := c.stderr.(*os.File)
	return &confirmer{
		out:         c.stderr,
		in:          bufio.NewReader(c.stdin),
		interactive: stdin != nil && stderr != nil && terminal.IsTerminal(stdin) && terminal.IsTerminal(stderr),
	}
}

// Confirm shows what would change and reads a line: "y" or "yes" is agreement,
// anything else (including end of input) is not.
func (c *confirmer) Confirm(_ context.Context, q syncbus.Question) (bool, error) {
	if !c.interactive {
		return false, syncbus.ErrNoTerminal
	}

	_, _ = fmt.Fprintf(c.out, "\nBaton does not own %s and wants to change it:\n\n%s\n", q.Path, q.Diff)
	_, _ = fmt.Fprintf(c.out, "If you agree, the current version is copied to ~/.baton/backups/%s/ first.\nChange %s? [y/N] ", q.Profile, q.Path)

	line, err := c.in.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	_, _ = fmt.Fprintln(c.out)
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}
