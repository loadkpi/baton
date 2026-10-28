package main

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/loadkpi/baton/business/types/status"
)

// ANSI sequences of the screen.
const (
	enterScreen = "\x1b[?1049h\x1b[?25l" // alternate screen, hide the cursor
	leaveScreen = "\x1b[?25h\x1b[?1049l" // show the cursor, back to the normal screen
	clearScreen = "\x1b[H\x1b[2J"
	reverse     = "\x1b[7m"
	boldYellow  = "\x1b[1;33m"
	boldRed     = "\x1b[1;31m"
	dim         = "\x1b[2m"
	plain       = "\x1b[0m"
)

// defaultWidth is the width of a terminal that does not say how wide it is.
const defaultWidth = 120

// maxCell is the widest a cell of the table is drawn; longer ones end in "…".
const maxCell = 24

// statusColumn is the index of the STATUS cell of a row of `baton ls`.
const statusColumn = 6

// wideWidth is the width from which `baton top` has the LAST column; under it
// the last action goes on a line of its own under the selected row.
const wideWidth = 100

// maxLong is the widest NAME and LAST are drawn: they take the room that is
// left, not a fixed share.
const maxLong = 200

// column is a column of the table: its title, the least it is narrowed to (0
// for a column that is never narrowed) and the widest it is drawn.
type column struct {
	title string
	least int
	limit int
}

// topColumns are the columns of the screen; LAST is one only when there is room.
func topColumns(wide bool) []column {
	cols := []column{
		{"PROJECT", 8, maxCell}, {"TASK", 12, maxCell}, {"AGENT", 0, maxCell}, {"SESSION", 0, maxCell},
		{"NAME", 8, maxLong},
	}
	if wide {
		cols = append(cols, column{"LAST", 8, maxLong})
	}
	return append(cols, column{"PROFILE", 6, maxCell}, column{"RUNTIME", 0, maxCell}, column{"STATUS", 0, maxCell})
}

// topCells are the cells of a row, for the columns of topColumns.
func topCells(l line, wide bool) []string {
	cells := longCells(l)
	if wide {
		return cells
	}
	return append(cells[:sessionColumn+2], cells[sessionColumn+3:]...) // without LAST
}

// topScreen is everything one frame of `baton top` shows.
type topScreen struct {
	lines    []line
	selected int // index into lines, -1 for none
	prompt   string
	message  string
	width    int
}

// waiting counts the Sessions that wait for the user.
func waiting(lines []line) int {
	n := 0
	for _, l := range lines {
		switch l.cells[statusColumn] {
		case status.WaitingApproval.String(), status.WaitingInput.String():
			n++
		}
	}
	return n
}

// render draws the whole screen: a header line, the table, the footer.
func (s topScreen) render() string {
	width := s.width
	if width <= 0 {
		width = defaultWidth
	}
	var out []string
	out = append(out, clip(fmt.Sprintf("baton top: %d %s, %d waiting for you", len(s.lines), plural(len(s.lines), "session"), waiting(s.lines)), width), "")

	wide := width >= wideWidth
	cols := topColumns(wide)
	cells := make([][]string, 0, len(s.lines)+1)
	header := make([]string, len(cols))
	least, flex := make([]int, len(cols)), make([]bool, len(cols))
	for i, c := range cols {
		header[i], least[i], flex[i] = c.title, c.least, c.limit == maxLong
	}
	cells = append(cells, header)
	for _, l := range s.lines {
		cells = append(cells, topCells(l, wide))
	}
	widths := make([]int, len(cols))
	for _, row := range cells {
		for i, c := range row {
			widths[i] = min(max(widths[i], utf8.RuneCountInString(c)), cols[i].limit)
		}
	}
	fitColumns(widths, least, flex, width-prefixWidth)
	for i, row := range cells {
		text := "    " + formatRow(row, widths)
		style := ""
		if i > 0 {
			l := s.lines[i-1]
			marker, color := markerOf(l.cells[statusColumn])
			cursor := "  "
			if i-1 == s.selected {
				cursor = "> "
				style = reverse
			}
			text = cursor + marker + " " + formatRow(row, widths)
			if style == "" {
				style = color
			}
		}
		text = clip(text, width)
		if style != "" {
			text = style + text + plain
		}
		out = append(out, text)
		if i > 0 && i-1 == s.selected && !wide {
			out = append(out, dim+clip(strings.Repeat(" ", prefixWidth)+s.lines[i-1].last, width)+plain)
		}
	}
	if len(s.lines) == 0 {
		out = append(out, "no sessions")
	}

	out = append(out, "")
	if s.prompt != "" {
		out = append(out, clip(s.prompt, width))
	} else {
		out = append(out, clip("j/k or arrows move   Enter jump to the tab   e end the session   q quit", width))
		out = append(out, clip(s.message, width))
	}
	return clearScreen + strings.Join(out, "\r\n")
}

// markerOf is the mark shown in front of a row, and the colour of the row, for
// a status; the mark makes the rows stand out where there are no colours.
func markerOf(st string) (marker, color string) {
	switch st {
	case status.WaitingApproval.String():
		return "!", boldYellow
	case status.Lost.String():
		return "x", boldRed
	}
	return " ", ""
}

// prefixWidth is the width of the cursor and the marker in front of a row.
const prefixWidth = 4

// fitColumns narrows the columns until they and the gaps between them fit in
// avail characters or none can be narrowed more: first NAME and LAST, which
// take the room that is left, then the others. Within a group the widest column
// is narrowed, one character at a time. least says to what a column may be
// narrowed, 0 for never; flex marks NAME and LAST.
func fitColumns(widths, least []int, flex []bool, avail int) {
	for _, phase := range []bool{true, false} {
		for !fits(widths, avail) {
			widest := -1
			for i, w := range widths {
				if flex[i] == phase && least[i] > 0 && w > least[i] && (widest < 0 || w > widths[widest]) {
					widest = i
				}
			}
			if widest < 0 {
				break
			}
			widths[widest]--
		}
	}
}

// fits reports whether the columns and the gaps between them fit in avail.
func fits(widths []int, avail int) bool {
	total := 2 * (len(widths) - 1)
	for _, w := range widths {
		total += w
	}
	return total <= avail
}

func formatRow(row []string, widths []int) string {
	parts := make([]string, len(row))
	for i, c := range row {
		c = clip(c, widths[i])
		parts[i] = c + strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c))
	}
	return strings.TrimRight(strings.Join(parts, "  "), " ")
}

// clip cuts s to n characters, ending it in "…" when something is cut.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	if n <= 0 {
		return ""
	}
	return string(r[:n-1]) + "…"
}
