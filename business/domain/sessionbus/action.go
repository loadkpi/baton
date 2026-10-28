package sessionbus

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// maxActionDetail is how many characters of a command, a prompt or a path the
// description of an Event keeps; the screen cuts it further to its width.
const maxActionDetail = 120

// toolDetailKeys are the fields of a tool call's input that say what it did,
// in the order they are tried.
var toolDetailKeys = []string{"command", "file_path", "pattern", "url"}

// actionPayload is the part of an Event's payload that describes an action.
type actionPayload struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
	Prompt    string         `json:"prompt"`
}

// Action describes what the Event reports in one short line, for a person who
// wants to know what a Session did last. It is read from the stored payload,
// where secrets are already masked, so none appears in it. An Event it has no
// words for is described by its name.
func (e Event) Action() string {
	switch e.Name {
	case "Stop":
		return "done"
	case "Interrupt":
		return "interrupted"
	case "SessionStart":
		return "started"
	case "SessionEnd":
		return "ended"
	}
	var p actionPayload
	_ = json.Unmarshal(e.Payload, &p) // a payload that is not an object describes nothing
	switch e.Name {
	case "UserPromptSubmit":
		return "prompt: " + OneLine(p.Prompt, maxActionDetail)
	case "PreToolUse", "PostToolUse":
		return p.tool("")
	case "PostToolUseFailure":
		return p.tool(" failed")
	case "PermissionRequest":
		return "asks: " + p.tool("")
	}
	return e.Name
}

// tool describes a tool call: "<tool>: <detail>", "skill <name>" for the Skill
// tool; suffix goes after the tool.
func (p actionPayload) tool(suffix string) string {
	if p.ToolName == "Skill" {
		for _, k := range []string{"skill", "name"} {
			if s, _ := p.ToolInput[k].(string); s != "" {
				return "skill " + OneLine(s, maxActionDetail) + suffix
			}
		}
	}
	name := p.ToolName
	if name == "" {
		name = "tool"
	}
	for _, k := range toolDetailKeys {
		if s, _ := p.ToolInput[k].(string); s != "" {
			return name + suffix + ": " + OneLine(s, maxActionDetail)
		}
	}
	return name + suffix
}

// OneLine puts s on one line (every run of white space becomes a space) and cuts
// it to n characters, ending it in "…" when something is cut.
func OneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:max(n-1, 0)]) + "…"
}

// Age is how long ago something was, in one unit: 12s, 4m, 3h, 2d.
func Age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(int(d/time.Second), 0))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}

// LastAction is the line that says what the Session did last: how long ago the
// newest Event came and what it reported ("4m Bash: go test ./..."). It is
// empty for a Session with no Event.
func (s Session) LastAction(now time.Time) string {
	if s.Newest.Name == "" {
		return ""
	}
	return Age(now.Sub(s.Newest.TS)) + " " + s.Newest.Action()
}

// transcriptOf is the transcript_path an Event's payload carries, or "".
func transcriptOf(e Event) string {
	var p struct {
		Transcript string `json:"transcript_path"`
	}
	_ = json.Unmarshal(e.Payload, &p)
	return p.Transcript
}

// FirstPrompt is the prompt of the first UserPromptSubmit Event of the Session,
// on one line; ok is false when it has none.
func (b *Bus) FirstPrompt(ctx context.Context, s Session) (prompt string, ok bool, err error) {
	err = b.storer.QueryEventsBackward(ctx, s.Ref, func(e Event) bool {
		if e.Name != "UserPromptSubmit" {
			return true
		}
		var p actionPayload
		if json.Unmarshal(e.Payload, &p) == nil && strings.TrimSpace(p.Prompt) != "" {
			prompt, ok = OneLine(p.Prompt, maxActionDetail), true // the oldest seen so far
		}
		return true
	})
	return prompt, ok, err
}
