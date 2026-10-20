// Package mdblock finds, writes and removes one marked block in a Markdown
// text, leaving every other byte of the text as it was.
//
// A block is two marker lines and the body between them:
//
//	<!-- begin -->
//	body
//	<!-- end -->
package mdblock

import (
	"errors"
	"fmt"
	"strings"
)

// Markers are the two lines that delimit a block. A marker is recognized only
// when it is the whole line (surrounding spaces and a carriage return aside).
type Markers struct {
	Begin string
	End   string
}

// Region is where a block sits in a text.
type Region struct {
	// Start is the offset of the begin marker line.
	Start int
	// End is the offset just after the end marker line and its newline.
	End int
	// Body is the text between the marker lines, without the newline that
	// precedes the end marker.
	Body string
}

// ErrBroken means the markers do not form exactly one block.
var ErrBroken = errors.New("broken block markers")

// Find returns the block in text. found is false when text has no markers at
// all. A begin marker without an end marker, an end marker before the begin
// marker, or more than one block is an error wrapping ErrBroken: nothing can be
// said about which part is whose.
func Find(text string, m Markers) (r Region, found bool, err error) {
	var begins, ends []lineAt
	offset := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		switch strings.TrimSpace(line) {
		case m.Begin:
			begins = append(begins, lineAt{offset, offset + len(line)})
		case m.End:
			ends = append(ends, lineAt{offset, offset + len(line)})
		}
		offset += len(line)
	}

	switch {
	case len(begins) == 0 && len(ends) == 0:
		return Region{}, false, nil
	case len(begins) != 1 || len(ends) != 1:
		return Region{}, false, fmt.Errorf("%w: found %d %q and %d %q", ErrBroken, len(begins), m.Begin, len(ends), m.End)
	case ends[0].start < begins[0].end:
		return Region{}, false, fmt.Errorf("%w: %q comes before %q", ErrBroken, m.End, m.Begin)
	}

	body := text[begins[0].end:ends[0].start]
	body = strings.TrimSuffix(body, "\n")
	return Region{Start: begins[0].start, End: ends[0].end, Body: body}, true, nil
}

type lineAt struct{ start, end int }

// render is the block as written: markers around body, each on its own line.
func render(body string, m Markers) string {
	if body != "" {
		body += "\n"
	}
	return m.Begin + "\n" + body + m.End + "\n"
}

// Append returns text with the block added at its end, separated from what
// precedes it by a blank line.
func Append(text, body string, m Markers) string {
	if text != "" {
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += "\n"
	}
	return text + render(body, m)
}

// Replace returns text with the block r given a new body.
func Replace(text string, r Region, body string, m Markers) string {
	return text[:r.Start] + render(body, m) + text[r.End:]
}

// Remove returns text without the block r. When the block closed the text, the
// blank line that Append put before it goes too, so a text that got a block
// and loses it is the text it was.
func Remove(text string, r Region) string {
	before, after := text[:r.Start], text[r.End:]
	if after == "" && strings.HasSuffix(before, "\n\n") {
		before = before[:len(before)-1]
	}
	return before + after
}
