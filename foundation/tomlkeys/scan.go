package tomlkeys

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/BurntSushi/toml"
)

// item is a table header or a key/value line (with the lines its value runs
// over), and where it sits in the document.
type item struct {
	header bool
	// array is set for [[a.b]].
	array bool
	// path is the table a header names, or the whole path of a key: the table
	// it sits in and then its own, possibly dotted, name.
	path []string
	// table is the table a key sits in.
	table []string
	// start is the first byte of the line; end is the byte after the last
	// line break of the item (or the end of the document).
	start, end int
}

type scanner struct {
	doc []byte
	pos int
}

// scan finds the headers and keys of a document that BurntSushi accepted, so
// it only has to find where things end, not to judge them. Comments and blank
// lines are not items.
func scan(doc []byte) ([]item, error) {
	s := &scanner{doc: doc}
	var items []item
	var table []string
	for s.pos < len(doc) {
		start := s.pos
		s.skipBlanks()
		switch {
		case s.pos >= len(doc):
		case s.atLineEnd():
			s.skipLineEnd()
		case doc[s.pos] == '#':
			s.skipComment()
		case doc[s.pos] == '[':
			it, err := s.header(start)
			if err != nil {
				return nil, err
			}
			table = it.path
			items = append(items, it)
		default:
			it, err := s.keyValue(start, table)
			if err != nil {
				return nil, err
			}
			items = append(items, it)
		}
	}
	return items, nil
}

func (s *scanner) skipBlanks() {
	for s.pos < len(s.doc) && (s.doc[s.pos] == ' ' || s.doc[s.pos] == '\t') {
		s.pos++
	}
}

func (s *scanner) atLineEnd() bool {
	return s.pos < len(s.doc) && (s.doc[s.pos] == '\n' || bytes.HasPrefix(s.doc[s.pos:], []byte("\r\n")))
}

func (s *scanner) skipLineEnd() {
	if s.doc[s.pos] == '\r' {
		s.pos++
	}
	s.pos++
}

// skipComment moves past the comment and its line break.
func (s *scanner) skipComment() {
	i := bytes.IndexByte(s.doc[s.pos:], '\n')
	if i < 0 {
		s.pos = len(s.doc)
		return
	}
	s.pos += i + 1
}

// endLine moves past what may follow an item on its line: white space, a
// comment, the line break.
func (s *scanner) endLine() error {
	s.skipBlanks()
	switch {
	case s.pos >= len(s.doc):
	case s.doc[s.pos] == '#':
		s.skipComment()
	case s.atLineEnd():
		s.skipLineEnd()
	default:
		return fmt.Errorf("unexpected %q after a value at byte %d", s.doc[s.pos], s.pos)
	}
	return nil
}

// header reads [a.b] or [[a.b]], whose line starts at start.
func (s *scanner) header(start int) (item, error) {
	from := s.pos
	array := bytes.HasPrefix(s.doc[s.pos:], []byte("[["))
	s.pos++
	if array {
		s.pos++
	}
	for s.pos < len(s.doc) && s.doc[s.pos] != ']' {
		if err := s.skipQuoted(); err != nil {
			return item{}, err
		}
	}
	s.pos++
	if array {
		s.pos++
	}
	path, err := pathOf(s.doc[from:min(s.pos, len(s.doc))])
	if err != nil {
		return item{}, err
	}
	if err := s.endLine(); err != nil {
		return item{}, err
	}
	return item{header: true, array: array, path: path, start: start, end: s.pos}, nil
}

// keyValue reads `key = value`, whose line starts at start, inside table.
func (s *scanner) keyValue(start int, table []string) (item, error) {
	from := s.pos
	for s.pos < len(s.doc) && s.doc[s.pos] != '=' {
		if err := s.skipQuoted(); err != nil {
			return item{}, err
		}
	}
	if s.pos >= len(s.doc) {
		return item{}, errors.New("a key without a value")
	}
	// The value is replaced by a number so that BurntSushi reads the key alone.
	name, err := pathOf(append(bytes.Clone(s.doc[from:s.pos]), []byte("= 1")...))
	if err != nil {
		return item{}, err
	}
	s.pos++
	s.skipBlanks()
	if err := s.skipValue(); err != nil {
		return item{}, err
	}
	if err := s.endLine(); err != nil {
		return item{}, err
	}
	path := append(append([]string(nil), table...), name...)
	return item{path: path, table: table, start: start, end: s.pos}, nil
}

// skipQuoted moves past the character at pos, or past the whole string when
// it opens one. Strings written with ”' or """ run over lines.
func (s *scanner) skipQuoted() error {
	c := s.doc[s.pos]
	if c != '"' && c != '\'' {
		s.pos++
		return nil
	}
	triple := bytes.HasPrefix(s.doc[s.pos:], bytes.Repeat([]byte{c}, 3))
	if triple {
		s.pos += 3
	} else {
		s.pos++
	}
	for s.pos < len(s.doc) {
		switch {
		case c == '"' && s.doc[s.pos] == '\\':
			s.pos += 2
		case triple && bytes.HasPrefix(s.doc[s.pos:], bytes.Repeat([]byte{c}, 3)):
			s.pos += 3
			// One or two quotes next to the closing ones belong to the string.
			for extra := 0; extra < 2 && s.pos < len(s.doc) && s.doc[s.pos] == c; extra++ {
				s.pos++
			}
			return nil
		case !triple && s.doc[s.pos] == c:
			s.pos++
			return nil
		default:
			s.pos++
		}
	}
	return errors.New("a string that does not end")
}

// skipValue moves past a value: a string, an array or inline table (which may
// run over lines and hold comments), or a bare number, boolean or date.
func (s *scanner) skipValue() error {
	if s.pos >= len(s.doc) {
		return errors.New("a key without a value")
	}
	switch s.doc[s.pos] {
	case '"', '\'':
		return s.skipQuoted()
	case '[', '{':
		depth := 0
		for s.pos < len(s.doc) {
			switch s.doc[s.pos] {
			case '[', '{':
				depth++
				s.pos++
			case ']', '}':
				depth--
				s.pos++
				if depth == 0 {
					return nil
				}
			case '"', '\'':
				if err := s.skipQuoted(); err != nil {
					return err
				}
			case '#':
				s.skipComment()
			default:
				s.pos++
			}
		}
		return errors.New("an array or inline table that does not end")
	}
	for s.pos < len(s.doc) && s.doc[s.pos] != '#' && s.doc[s.pos] != '\n' && s.doc[s.pos] != '\r' {
		s.pos++
	}
	return nil
}

// pathOf reads a header (`[a."b.c"]`, `[[a]]`) or a key (`a.b = 1`) with
// BurntSushi and returns the names in it.
func pathOf(text []byte) ([]string, error) {
	var m map[string]any
	if _, err := toml.Decode(string(text)+"\n", &m); err != nil {
		return nil, fmt.Errorf("read %q: %w", text, err)
	}
	var path []string
	var cur any = m
	for {
		if list, ok := cur.([]map[string]any); ok && len(list) == 1 {
			cur = list[0]
		}
		table, ok := cur.(map[string]any)
		if !ok || len(table) != 1 {
			return path, nil
		}
		for name, v := range table {
			path = append(path, name)
			cur = v
		}
	}
}
