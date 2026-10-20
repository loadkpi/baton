package handoffbus

import (
	"fmt"
	"strings"
	"time"

	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/foundation/git"
)

// Narrative says who wrote the narrative part of a Handoff, and so how far to
// trust it (spec story 55).
type Narrative string

// The values of the `narrative` key of the header.
const (
	// NarrativeHandingOver: the Session that was handing the Task over wrote it.
	NarrativeHandingOver Narrative = "handing-over"
	// NarrativeReceiving: the receiving Session wrote it (ticket 15).
	NarrativeReceiving Narrative = "receiving"
	// NarrativeMissing: nobody has written it yet.
	NarrativeMissing Narrative = "missing"
)

// Headings that ticket 15 finds the narrative part by. The narrative part is the
// last section of the file and runs to its end, so that a Markdown heading in
// the text a Session wrote cannot be mistaken for the end of it.
const (
	// NarrativeHeading is the line that starts the narrative part.
	NarrativeHeading = "## Narrative"
	// missingText stands where the narrative part will be written.
	missingText = "_Not written yet. The Session that handed the Task over did not save a narrative; the receiving Session writes it with `baton handoff save`._"
	// maxCommandBytes keeps a command that is mostly data (a heredoc that
	// writes a file) from filling the Handoff.
	maxCommandBytes = 1000
)

// document is everything that goes into a Handoff file.
type document struct {
	Number  int
	Created time.Time

	Project     string
	Task        string
	TaskName    string
	WorkingCopy string

	// From is the Session that is handed over; To is where the Task goes.
	From      sessionbus.Session
	EventFile string
	ToAgent   string
	ToProfile string
	ToRuntime string

	Git      git.Snapshot
	Commands []sessionbus.Command

	Narrative     Narrative
	NarrativeText string
}

// render writes the document: a header, the automatic part (facts taken after the
// old Session ended) and the narrative part.
func (d document) render() []byte {
	var b strings.Builder
	label := d.Project + "/" + d.Task

	b.WriteString("---\n")
	fmt.Fprintf(&b, "task: %s\n", label)
	fmt.Fprintf(&b, "handoff: %d\n", d.Number)
	fmt.Fprintf(&b, "narrative: %s\n", d.Narrative)
	fmt.Fprintf(&b, "created: %s\n", d.Created.UTC().Format(time.RFC3339))
	b.WriteString("---\n\n")

	fmt.Fprintf(&b, "# Handoff %d of %s\n\n", d.Number, label)
	b.WriteString("## Facts\n\n")
	b.WriteString("Taken by `baton handoff` after the handing-over Session ended.\n\n")
	fmt.Fprintf(&b, "- Task: %s\n", d.Task)
	if d.TaskName != "" {
		fmt.Fprintf(&b, "- Task name: %s\n", d.TaskName)
	}
	fmt.Fprintf(&b, "- Project: %s\n", d.Project)
	fmt.Fprintf(&b, "- Working copy: %s\n", d.WorkingCopy)
	fmt.Fprintf(&b, "- Branch: %s\n", orElse(d.Git.Branch, "(detached HEAD)"))
	if d.Git.Head == "" {
		b.WriteString("- HEAD: (no commit yet)\n")
	} else {
		fmt.Fprintf(&b, "- HEAD: %s %s\n", d.Git.Head, d.Git.Subject)
	}
	s := d.From
	fmt.Fprintf(&b, "- Handed over by: %s, profile %s, runtime %s, session %s (%s)\n", s.Agent, s.Profile, s.Runtime, s.SessionID, s.Status)
	fmt.Fprintf(&b, "- Event file of that Session: %s\n", d.EventFile)
	fmt.Fprintf(&b, "- Handed over to: %s, profile %s, runtime %s\n\n", d.ToAgent, d.ToProfile, d.ToRuntime)

	b.WriteString("### git status\n\n")
	b.WriteString(fenced(orElse(d.Git.Status, "(clean)")))
	b.WriteString("\n### git diff --stat\n\n")
	b.WriteString(fenced(orElse(d.Git.DiffStat, "(no unstaged changes)")))
	if d.Git.StagedStat != "" {
		b.WriteString("\n### git diff --cached --stat\n\n")
		b.WriteString(fenced(d.Git.StagedStat))
	}

	b.WriteString("\n### Last commands that finished (up to 10), oldest first\n\n")
	if len(d.Commands) == 0 {
		b.WriteString("(none recorded)\n")
	} else {
		b.WriteString("An exit code of `?` means the Agent's hook does not report it.\n\n")
	}
	for i, c := range d.Commands {
		fmt.Fprintf(&b, "%d. exit %s, %s\n\n", i+1, c.Exit, c.TS.UTC().Format(time.RFC3339))
		b.WriteString(fenced(truncate(c.Line, maxCommandBytes)))
		b.WriteString("\n")
	}

	b.WriteString("\n" + NarrativeHeading + "\n\n")
	if d.Narrative == NarrativeMissing {
		b.WriteString(missingText + "\n")
	} else {
		b.WriteString(strings.TrimSpace(d.NarrativeText) + "\n")
	}
	return []byte(b.String())
}

func orElse(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// fenced puts text in a code block whose fence is longer than any run of
// backticks in it.
func fenced(text string) string {
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", max(3, longest+1))
	return fence + "\n" + strings.TrimRight(text, "\n") + "\n" + fence + "\n"
}

// truncate shortens s to at most n bytes, on a character boundary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return fmt.Sprintf("%s\n... (%d more bytes not shown)", s[:cut], len(s)-cut)
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
