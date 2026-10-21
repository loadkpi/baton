// Package tomlkeys changes named tables of a TOML document, with the tables
// below them, and leaves every other byte of it as it was: comments, white
// space, the order of the tables, the formatting of the values it does not
// touch.
//
// It is the counterpart of foundation/jsonkeys for a format whose tables are
// spread over a file. It parses just enough to find where each table header
// and each key/value line begins and ends (strings, arrays and inline tables
// that run over several lines included), and splices by those byte ranges.
// github.com/BurntSushi/toml reads the values and, after every edit, proves
// that the result is still valid TOML; it cannot write while keeping bytes,
// which is why the splicing is done here.
//
// A table is addressed by its path, for example ["mcp_servers", "tracker"],
// and means the header [mcp_servers.tracker] together with the sub-tables
// [mcp_servers.tracker.env], wherever in the file they are. The package
// refuses, rather than guesses, when the same table is spelled in a form it
// does not edit: dotted keys (`mcp_servers.tracker.command = "x"`), an inline
// table (`tracker = { command = "x" }`) or an array of tables.
package tomlkeys

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// Get returns the table at key, with the tables below it, as BurntSushi reads
// them. found is false when the document has no such table.
func Get(doc []byte, key []string) (table map[string]any, found bool, err error) {
	d, err := open(doc)
	if err != nil {
		return nil, false, err
	}
	spans, err := d.spans(key)
	if err != nil || len(spans) == 0 {
		return nil, false, err
	}
	var text bytes.Buffer
	for _, s := range spans {
		text.Write(doc[s.from:s.to])
		if !bytes.HasSuffix(text.Bytes(), []byte("\n")) {
			text.WriteString("\n")
		}
	}
	var root map[string]any
	if _, err := toml.Decode(text.String(), &root); err != nil {
		return nil, false, fmt.Errorf("read %s: %w", strings.Join(key, "."), err)
	}
	var cur any = root
	for _, k := range key {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("%s is not a table", strings.Join(key, "."))
		}
		cur = m[k]
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return nil, false, fmt.Errorf("%s is not a table", strings.Join(key, "."))
	}
	return m, true, nil
}

// Set writes table at key. A table that is there is replaced where it stands
// (its sub-tables, wherever they are, with it); a new one is appended after
// everything else, separated by a blank line.
func Set(doc []byte, key []string, table map[string]any) ([]byte, error) {
	d, err := open(doc)
	if err != nil {
		return nil, err
	}
	spans, err := d.spans(key)
	if err != nil {
		return nil, err
	}
	text, err := Format(key, table)
	if err != nil {
		return nil, err
	}
	text = strings.ReplaceAll(text, "\n", d.eol)

	var out []byte
	if len(spans) == 0 {
		out = appendTable(doc, text, d.eol)
	} else {
		out = slices.Clone(doc)
		for i := len(spans) - 1; i > 0; i-- {
			out = cut(out, spans[i])
		}
		out = splice(out, spans[0].from, spans[0].to, []byte(text))
	}
	return check(out, key)
}

// Delete removes the table at key and the tables below it, and the blank
// lines that separated them from what follows. It returns the document
// unchanged when the table is not there.
func Delete(doc []byte, key []string) ([]byte, error) {
	d, err := open(doc)
	if err != nil {
		return nil, err
	}
	spans, err := d.spans(key)
	if err != nil || len(spans) == 0 {
		return doc, err
	}
	out := slices.Clone(doc)
	for i := len(spans) - 1; i >= 0; i-- {
		out = cut(out, spans[i])
	}
	return check(out, key)
}

// Format renders table as the text Set writes for it: the header [a.b] of key
// (a.b), its keys and the headers of its sub-tables, with no indentation, and
// no header for the parents of key. The text ends with a newline.
func Format(key []string, table map[string]any) (string, error) {
	if len(key) == 0 {
		return "", errors.New("a table needs a key")
	}
	var nested any = table
	for i := len(key) - 1; i >= 0; i-- {
		nested = map[string]any{key[i]: nested}
	}
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	enc.Indent = ""
	if err := enc.Encode(nested); err != nil {
		return "", fmt.Errorf("render %s: %w", strings.Join(key, "."), err)
	}
	// The encoder opens with the headers of the parents; the table starts at
	// its own.
	text := buf.String()
	d, err := open([]byte(text))
	if err != nil {
		return "", err
	}
	var starts []int
	for _, it := range d.items {
		if it.header && !it.array && slices.Equal(it.path, key) {
			starts = append(starts, it.start)
		}
		if len(starts) > 0 && it.header && it.start != starts[0] {
			starts = append(starts, it.start)
		}
	}
	if len(starts) == 0 {
		return "", fmt.Errorf("render %s: the encoder wrote no header for it", strings.Join(key, "."))
	}
	// A blank line before each sub-table, which the encoder does not write.
	starts = append(starts, len(text))
	var out strings.Builder
	for i := 0; i < len(starts)-1; i++ {
		if i > 0 {
			out.WriteString("\n")
		}
		out.WriteString(strings.TrimRight(text[starts[i]:starts[i+1]], "\n") + "\n")
	}
	return out.String(), nil
}

// check proves that the edited document is still valid TOML, and that the
// table at key is still found in one piece.
func check(out []byte, key []string) ([]byte, error) {
	d, err := open(out)
	if err != nil {
		return nil, fmt.Errorf("the edit would leave invalid TOML (nothing was written): %w", err)
	}
	if _, err := d.spans(key); err != nil {
		return nil, fmt.Errorf("the edit would leave a table Baton cannot find again (nothing was written): %w", err)
	}
	return out, nil
}

// appendTable adds text after everything in doc, with one blank line between.
func appendTable(doc []byte, text, eol string) []byte {
	out := slices.Clone(doc)
	if len(bytes.TrimRight(out, " \t\r\n")) > 0 {
		if !bytes.HasSuffix(out, []byte("\n")) {
			out = append(out, eol...)
		}
		tail := out[len(bytes.TrimRight(out, " \t\r\n")):]
		if bytes.Count(tail, []byte("\n")) < 2 {
			out = append(out, eol...)
		}
	}
	return append(out, text...)
}

// cut removes a span and the blank lines after it. When nothing but blank
// lines followed, the blank lines before it go too: they only separated it
// from what is no longer there.
func cut(doc []byte, s span) []byte {
	from, to := s.from, s.to
	for next, ok := blankLine(doc, to); ok; next, ok = blankLine(doc, to) {
		to = next
	}
	if to == len(doc) {
		for from > 0 {
			prev := lineBefore(doc, from)
			if _, ok := blankLine(doc, prev); !ok {
				break
			}
			from = prev
		}
	}
	return splice(doc, from, to, nil)
}

func splice(doc []byte, from, to int, text []byte) []byte {
	out := make([]byte, 0, len(doc)-(to-from)+len(text))
	out = append(out, doc[:from]...)
	out = append(out, text...)
	return append(out, doc[to:]...)
}

// blankLine reports whether the line that starts at pos holds only white
// space, and where the next one starts.
func blankLine(doc []byte, pos int) (next int, ok bool) {
	if pos >= len(doc) {
		return pos, false
	}
	i := pos
	for i < len(doc) && (doc[i] == ' ' || doc[i] == '\t' || doc[i] == '\r') {
		i++
	}
	switch {
	case i == len(doc):
		return i, true
	case doc[i] == '\n':
		return i + 1, true
	}
	return pos, false
}

// lineBefore is the start of the line that ends where the line at pos starts.
func lineBefore(doc []byte, pos int) int {
	if pos == 0 {
		return 0
	}
	return bytes.LastIndexByte(doc[:pos-1], '\n') + 1
}

// span is a byte range of the document: from the start of a table header's
// line to the end of the last key/value line before the next header. Comments
// and blank lines after the last key do not belong to it.
type span struct{ from, to int }

type doc struct {
	data  []byte
	eol   string
	items []item
}

// open checks the document with BurntSushi and finds its headers and keys.
func open(data []byte) (*doc, error) {
	var parsed map[string]any
	if _, err := toml.Decode(string(data), &parsed); err != nil {
		return nil, fmt.Errorf("not valid TOML: %w", err)
	}
	d := &doc{data: data, eol: "\n"}
	if crlf := bytes.Count(data, []byte("\r\n")); crlf > 0 {
		if crlf != bytes.Count(data, []byte("\n")) {
			return nil, errors.New("the file mixes CRLF and LF line endings; make them the same and run it again")
		}
		d.eol = "\r\n"
	}
	items, err := scan(data)
	if err != nil {
		return nil, err
	}
	d.items = items
	return d, nil
}

// spans finds the ranges that hold the table at key and the tables below it.
// It fails when the same table is also spelled some way that is not edited.
func (d *doc) spans(key []string) ([]span, error) {
	if len(key) == 0 {
		return nil, errors.New("a table needs a key")
	}
	var spans []span
	cur := -1 // the span the last header opened, while it is in the table
	for _, it := range d.items {
		if it.header {
			if it.array && hasPrefix(key, it.path) {
				return nil, d.refuse(key, it, "an array of tables")
			}
			if !hasPrefix(it.path, key) {
				cur = -1
				continue
			}
			if cur < 0 {
				spans = append(spans, span{from: it.start})
				cur = len(spans) - 1
			}
			spans[cur].to = it.end
			continue
		}
		if hasPrefix(it.table, key) {
			spans[cur].to = it.end // the body of the table
			continue
		}
		if hasPrefix(it.path, key) || hasPrefix(key, it.path) {
			return nil, d.refuse(key, it, "dotted keys or an inline table")
		}
	}
	return spans, nil
}

// refuse is the error for a table that is spelled in a form that is not
// edited.
func (d *doc) refuse(key []string, it item, what string) error {
	line := 1 + bytes.Count(d.data[:it.start], []byte("\n"))
	name := strings.Join(key, ".")
	return fmt.Errorf("line %d: %s is written as %s, not as a [%s] table; Baton edits only the table form, so change it there by hand or remove it", line, name, what, name)
}

// hasPrefix reports whether path begins with prefix.
func hasPrefix(path, prefix []string) bool {
	return len(path) >= len(prefix) && slices.Equal(path[:len(prefix)], prefix)
}
