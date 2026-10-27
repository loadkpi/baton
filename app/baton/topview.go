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
	plain       = "\x1b[0m"
)

// defaultWidth is the width of a terminal that does not say how wide it is.
const defaultWidth = 80

// maxCell is the widest a cell of the table is drawn; longer ones end in "…".
const maxCell = 24

var topHeader = []string{"PROJECT", "TASK", "AGENT", "SESSION", "PROFILE", "RUNTIME", "STATUS"}

// statusColumn is the index of the STATUS cell of a row.
const statusColumn = 6

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

	cells := make([][]string, 0, len(s.lines)+1)
	cells = append(cells, topHeader)
	for _, l := range s.lines {
		cells = append(cells, l.cells)
	}
	widths := make([]int, len(topHeader))
	for _, row := range cells {
		for i, c := range row {
			widths[i] = min(max(widths[i], utf8.RuneCountInString(c)), maxCell)
		}
	}
	fitColumns(widths, width-prefixWidth)
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

// minShrunk maps the columns that may be narrowed to fit the terminal to the
// least they are narrowed to; the others are short or matter too much (STATUS,
// and the generated Task id) to be cut.
var minShrunk = map[int]int{0: 8, 1: 12, 4: 6}

// fitColumns narrows the widest shrinkable column, one character at a time,
// until the columns and the gaps between them fit in avail characters or none
// can be narrowed more.
func fitColumns(widths []int, avail int) {
	for {
		total := 2 * (len(widths) - 1)
		for _, w := range widths {
			total += w
		}
		if total <= avail {
			return
		}
		widest := -1
		for i, least := range minShrunk {
			if widths[i] > least && (widest < 0 || widths[i] > widths[widest]) {
				widest = i
			}
		}
		if widest < 0 {
			return
		}
		widths[widest]--
	}
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
