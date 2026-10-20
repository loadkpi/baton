// Package status defines the status of a Session and the one table that
// derives it from the Events an Agent reports.
package status

import (
	"sort"

	"github.com/loadkpi/baton/business/types/agent"
)

// Status says what a Session is doing, as far as its last Event tells.
type Status string

// The statuses of a Session (DESIGN.md, D2).
const (
	Running         Status = "running"
	WaitingApproval Status = "waiting_approval"
	WaitingInput    Status = "waiting_input"
	Ended           Status = "ended"
	// Unknown is for a Session whose Events are all of a kind this table does
	// not know. A defensive fallback: Baton installs hooks only for known Events.
	Unknown Status = "unknown"
)

// String returns the status as it is shown.
func (s Status) String() string { return string(s) }

// claudeTable is the Claude Code column of the status table in DESIGN.md.
var claudeTable = map[string]Status{
	"UserPromptSubmit":   Running,
	"PreToolUse":         Running,
	"PostToolUse":        Running,
	"PostToolUseFailure": Running,
	"PermissionRequest":  WaitingApproval,
	"SessionStart":       WaitingInput,
	"Stop":               WaitingInput,
	"SessionEnd":         Ended,
}

// codexTable is the Codex column of the same table. Sync installs its Events
// as hooks, and `baton ls` derives the status of a Codex Session from them.
var codexTable = map[string]Status{
	"UserPromptSubmit":  Running,
	"PreToolUse":        Running,
	"PostToolUse":       Running,
	"PermissionRequest": WaitingApproval,
	"SessionStart":      WaitingInput,
	"Stop":              WaitingInput,
	"Interrupt":         WaitingInput,
	"SessionEnd":        Ended,
}

func tableOf(a agent.Agent) map[string]Status {
	switch a {
	case agent.Claude:
		return claudeTable
	case agent.Codex:
		return codexTable
	default:
		return nil
	}
}

// FromEvent returns the status that an Event of the Agent puts a Session in.
// ok is false for an Event that says nothing about the status.
func FromEvent(a agent.Agent, event string) (s Status, ok bool) {
	s, ok = tableOf(a)[event]
	return s, ok
}

// HookEvents are the Events whose hooks the Agent must report for the
// statuses to be derived, sorted.
func HookEvents(a agent.Agent) []string {
	table := tableOf(a)
	events := make([]string, 0, len(table))
	for event := range table {
		events = append(events, event)
	}
	sort.Strings(events)
	return events
}
