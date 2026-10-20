// Package textdiff renders the difference between two texts as a unified diff.
package textdiff

import (
	"fmt"
	"strings"
)

// contextLines is how many unchanged lines surround each change.
const contextLines = 3

// maxCells bounds the table of the line matching, so a huge pair of texts
// cannot use unbounded memory. Beyond it the texts are shown as fully replaced.
const maxCells = 16 << 20

type op struct {
	kind byte // ' ' unchanged, '-' removed, '+' added
	line string
}

// Unified returns the difference between old and new in the unified diff
// format, with the given file names in its header. It returns "" when the texts
// are equal. A binary text (one with a NUL byte) is reported as such.
func Unified(oldName, newName, old, new string) string {
	if old == new {
		return ""
	}
	if strings.ContainsRune(old, 0) || strings.ContainsRune(new, 0) {
		return fmt.Sprintf("Binary files %s and %s differ\n", oldName, newName)
	}

	ops := diffLines(splitLines(old), splitLines(new))

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", oldName, newName)
	for _, h := range hunks(ops) {
		writeHunk(&b, ops, h)
	}
	return b.String()
}

// splitLines splits text into lines that keep their terminating newline, so a
// last line without one differs from the same line with one.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// diffLines is the edit script from a to b: the unchanged head and tail are
// peeled off and the middle is matched by a longest common subsequence.
func diffLines(a, b []string) []op {
	head := 0
	for head < len(a) && head < len(b) && a[head] == b[head] {
		head++
	}
	tail := 0
	for tail < len(a)-head && tail < len(b)-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		tail++
	}
	midA, midB := a[head:len(a)-tail], b[head:len(b)-tail]

	var ops []op
	for _, l := range a[:head] {
		ops = append(ops, op{' ', l})
	}
	ops = append(ops, lcsOps(midA, midB)...)
	for _, l := range a[len(a)-tail:] {
		ops = append(ops, op{' ', l})
	}
	return ops
}

func lcsOps(a, b []string) []op {
	var ops []op
	if len(a) == 0 || len(b) == 0 || (len(a)+1)*(len(b)+1) > maxCells {
		for _, l := range a {
			ops = append(ops, op{'-', l})
		}
		for _, l := range b {
			ops = append(ops, op{'+', l})
		}
		return ops
	}

	// table[i][j] is the length of the LCS of a[i:] and b[j:].
	table := make([][]int32, len(a)+1)
	for i := range table {
		table[i] = make([]int32, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else {
				table[i][j] = max(table[i+1][j], table[i][j+1])
			}
		}
	}

	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{' ', a[i]})
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			ops = append(ops, op{'-', a[i]})
			i++
		default:
			ops = append(ops, op{'+', b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		ops = append(ops, op{'-', a[i]})
	}
	for ; j < len(b); j++ {
		ops = append(ops, op{'+', b[j]})
	}
	return ops
}

// hunk is a range of ops, [from, to), that is shown together.
type hunk struct{ from, to int }

// hunks groups the changes with their context; changes separated by at most
// twice the context lines share a hunk.
func hunks(ops []op) []hunk {
	var changes []int
	for i, o := range ops {
		if o.kind != ' ' {
			changes = append(changes, i)
		}
	}

	var out []hunk
	for i := 0; i < len(changes); {
		first, last := changes[i], changes[i]
		i++
		for i < len(changes) && changes[i]-last-1 <= 2*contextLines {
			last = changes[i]
			i++
		}
		out = append(out, hunk{from: max(first-contextLines, 0), to: min(last+1+contextLines, len(ops))})
	}
	return out
}

func writeHunk(b *strings.Builder, ops []op, h hunk) {
	oldStart, newStart := 1, 1
	for _, o := range ops[:h.from] {
		if o.kind != '+' {
			oldStart++
		}
		if o.kind != '-' {
			newStart++
		}
	}
	oldCount, newCount := 0, 0
	for _, o := range ops[h.from:h.to] {
		if o.kind != '+' {
			oldCount++
		}
		if o.kind != '-' {
			newCount++
		}
	}
	// A range with no lines starts at the line before it.
	if oldCount == 0 {
		oldStart--
	}
	if newCount == 0 {
		newStart--
	}

	fmt.Fprintf(b, "@@ -%s +%s @@\n", rangeText(oldStart, oldCount), rangeText(newStart, newCount))
	for _, o := range ops[h.from:h.to] {
		b.WriteByte(o.kind)
		b.WriteString(o.line)
		if !strings.HasSuffix(o.line, "\n") {
			b.WriteString("\n\\ No newline at end of file\n")
		}
	}
}

func rangeText(start, count int) string {
	if count == 1 {
		return fmt.Sprint(start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}
