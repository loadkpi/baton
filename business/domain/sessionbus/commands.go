package sessionbus

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"time"

	"github.com/loadkpi/baton/business/types/agent"
)

// ExitCode is how a shell command ended, as far as the Events say.
type ExitCode struct {
	Code int
	// Known is false when the Events do not tell.
	Known bool
}

// String is the code, or "?" when it is not known.
func (e ExitCode) String() string {
	if !e.Known {
		return "?"
	}
	return strconv.Itoa(e.Code)
}

// Command is a shell command an Agent ran in a Session.
type Command struct {
	TS   time.Time
	Line string
	Exit ExitCode
}

// Commands returns the last n shell commands that finished in the Session, the
// oldest first. A command that was still running when the Session stopped has
// no finishing Event and is not among them.
//
// Where the exit code comes from (DESIGN.md, D5; both vendors' hook reference,
// read Oct 20, 2026):
//
//   - Claude Code: `PostToolUse` fires only after a tool succeeded, and the
//     docs give Bash's `tool_response` no exit code (it has stdout, stderr,
//     interrupted, isImage), so a command that got there exited with 0
//     [inferred from that rule, not seen in a recording]. A command that failed
//     gets `PostToolUseFailure`, whose `error` starts with the line `Exit code N`
//     (documented, and the docs say to key on it); a failure without that line
//     (an interrupt) has no known code.
//   - Codex: the shape of `tool_response` is not documented ("tool-specific
//     output"), and the hook also runs after a command that exits non-zero, so
//     the code is never known.
func (b *Bus) Commands(ctx context.Context, s Session, n int) ([]Command, error) {
	var found []Command
	err := b.storer.QueryEventsBackward(ctx, s.Ref, func(e Event) bool {
		if c, ok := toBusCommand(e); ok {
			found = append(found, c)
		}
		return len(found) < n
	})
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(found)-1; i < j; i, j = i+1, j-1 {
		found[i], found[j] = found[j], found[i]
	}
	return found, nil
}

var exitCodeLine = regexp.MustCompile(`^Exit code (-?\d+)(?:\n|$)`)

// toBusCommand reads a finished shell command from an Event, if it is one.
func toBusCommand(e Event) (Command, bool) {
	if e.Name != "PostToolUse" && e.Name != "PostToolUseFailure" {
		return Command{}, false
	}
	var p struct {
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
		Error string `json:"error"`
	}
	if json.Unmarshal(e.Payload, &p) != nil || p.ToolName != "Bash" || p.ToolInput.Command == "" {
		return Command{}, false
	}

	c := Command{TS: e.TS, Line: p.ToolInput.Command}
	switch {
	case e.Agent != agent.Claude:
	case e.Name == "PostToolUse":
		c.Exit = ExitCode{Code: 0, Known: true}
	default:
		if m := exitCodeLine.FindStringSubmatch(p.Error); m != nil {
			if code, err := strconv.Atoi(m[1]); err == nil {
				c.Exit = ExitCode{Code: code, Known: true}
			}
		}
	}
	return c, true
}

// EventFile is the path on the host of the file that holds the Session's
// Events.
func (b *Bus) EventFile(s Session) string {
	return b.storer.QueryPath(s.Ref)
}
