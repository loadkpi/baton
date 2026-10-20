// Package jsonkeys changes named keys of a JSON document, and elements of the
// arrays it holds, and leaves every other byte of it as it was: the white
// space, the order of the keys, the formatting of the values it does not touch.
package jsonkeys

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// node is a JSON value and where it sits in the document.
type node struct {
	start, end int
	// members are the members of an object; isObject tells it from other values.
	isObject bool
	members  []member
	// elems are the elements of an array.
	isArray bool
	elems   []node
}

type member struct {
	key      string
	keyStart int
	keyEnd   int
	value    node
}

type parser struct {
	doc []byte
	pos int
}

func (p *parser) skipSpace() {
	for p.pos < len(p.doc) && strings.IndexByte(" \t\r\n", p.doc[p.pos]) >= 0 {
		p.pos++
	}
}

// parseValue reads the value at the current position. The document is valid
// JSON (the caller checked), so it only has to find where values end.
func (p *parser) parseValue() (node, error) {
	p.skipSpace()
	n := node{start: p.pos}
	switch p.doc[p.pos] {
	case '{':
		n.isObject = true
		p.pos++
		for {
			p.skipSpace()
			if p.doc[p.pos] == '}' {
				p.pos++
				break
			}
			if p.doc[p.pos] == ',' {
				p.pos++
				continue
			}
			keyStart := p.pos
			key, err := p.parseString()
			if err != nil {
				return node{}, err
			}
			keyEnd := p.pos
			p.skipSpace()
			p.pos++ // the colon
			value, err := p.parseValue()
			if err != nil {
				return node{}, err
			}
			n.members = append(n.members, member{key: key, keyStart: keyStart, keyEnd: keyEnd, value: value})
		}
	case '[':
		n.isArray = true
		p.pos++
		for {
			p.skipSpace()
			if p.doc[p.pos] == ']' {
				p.pos++
				break
			}
			if p.doc[p.pos] == ',' {
				p.pos++
				continue
			}
			elem, err := p.parseValue()
			if err != nil {
				return node{}, err
			}
			n.elems = append(n.elems, elem)
		}
	case '"':
		if _, err := p.parseString(); err != nil {
			return node{}, err
		}
	default:
		for p.pos < len(p.doc) && strings.IndexByte(",}] \t\r\n", p.doc[p.pos]) < 0 {
			p.pos++
		}
	}
	n.end = p.pos
	return n, nil
}

// parseString reads the string literal at the current position and returns its
// decoded value.
func (p *parser) parseString() (string, error) {
	start := p.pos
	p.pos++
	for p.doc[p.pos] != '"' {
		if p.doc[p.pos] == '\\' {
			p.pos++
		}
		p.pos++
	}
	p.pos++
	var s string
	if err := json.Unmarshal(p.doc[start:p.pos], &s); err != nil {
		return "", err
	}
	return s, nil
}

func parse(doc []byte) (node, error) {
	if !json.Valid(doc) {
		var v any
		if err := json.Unmarshal(doc, &v); err != nil {
			return node{}, err
		}
		return node{}, errors.New("not valid JSON")
	}
	p := &parser{doc: doc}
	root, err := p.parseValue()
	if err != nil {
		return node{}, err
	}
	if !root.isObject {
		return node{}, errors.New("the top level is not an object")
	}
	return root, nil
}

// lineIndent is the white space that starts the line containing offset.
func lineIndent(doc []byte, offset int) string {
	start := bytes.LastIndexByte(doc[:offset], '\n') + 1
	end := start
	for end < len(doc) && (doc[end] == ' ' || doc[end] == '\t') {
		end++
	}
	return string(doc[start:end])
}

// Get returns the value at path, exactly as the document has it.
func Get(doc []byte, path []string) (raw []byte, found bool, err error) {
	root, err := parse(doc)
	if err != nil {
		return nil, false, err
	}
	cur := root
	for i, key := range path {
		next := find(cur, key)
		if next == nil || !next.value.isObject && i < len(path)-1 {
			return nil, false, nil
		}
		cur = next.value
	}
	return doc[cur.start:cur.end], true, nil
}

// Set makes the object member at path hold value (compact JSON). A member that
// exists is replaced; one that does not is added to the innermost existing
// object on the path, with the objects below it created.
func Set(doc []byte, path []string, value []byte) ([]byte, error) {
	root, err := parse(doc)
	if err != nil {
		return nil, err
	}

	cur, parent := root, root
	var existing *member
	i := 0
	for ; i < len(path); i++ {
		next := find(cur, path[i])
		if next == nil {
			break
		}
		if !next.value.isObject && i < len(path)-1 {
			return nil, fmt.Errorf("%q is not an object", strings.Join(path[:i+1], "."))
		}
		parent, cur, existing = cur, next.value, next
	}

	if i == len(path) {
		if !multiline(doc, parent) {
			return splice(doc, existing.value.start, existing.value.end, value), nil
		}
		indent := lineIndent(doc, existing.keyStart)
		unit := strings.TrimPrefix(indent, lineIndent(doc, parent.start))
		var formatted bytes.Buffer
		if err := json.Indent(&formatted, value, indent, unit); err != nil {
			return nil, err
		}
		return splice(doc, existing.value.start, existing.value.end, formatted.Bytes()), nil
	}

	// Wrap the value in the objects that are missing.
	nested := value
	for j := len(path) - 1; j > i; j-- {
		key, _ := json.Marshal(path[j])
		nested = append(append(append([]byte("{"), key...), ':'), append(nested, '}')...)
	}
	return insert(doc, cur, path[i], nested)
}

// insert adds the member key: value (compact JSON) to the object obj, written
// the way the object's other members are.
func insert(doc []byte, obj node, name string, value []byte) ([]byte, error) {
	key, _ := json.Marshal(name)
	var out bytes.Buffer

	if len(obj.members) == 0 {
		// An empty object is opened up, one member per line.
		outer := lineIndent(doc, obj.start)
		indent := outer + docUnit(doc)
		var formatted bytes.Buffer
		if err := json.Indent(&formatted, value, indent, docUnit(doc)); err != nil {
			return nil, err
		}
		out.Write(doc[:obj.start+1])
		out.WriteString("\n" + indent)
		out.Write(key)
		out.WriteString(": ")
		out.Write(formatted.Bytes())
		out.WriteString("\n" + outer)
		out.Write(doc[obj.end-1:])
		return out.Bytes(), nil
	}

	first, last := obj.members[0], obj.members[len(obj.members)-1]
	if !multiline(doc, obj) {
		// An object on one line: the new member is written like the first one.
		colon := string(doc[first.keyEnd:first.value.start])
		comma := ","
		if strings.HasSuffix(colon, " ") {
			comma = ", "
		}
		out.Write(doc[:last.value.end])
		out.WriteString(comma)
		out.Write(key)
		out.WriteString(colon)
		out.Write(value)
		out.Write(doc[last.value.end:])
		return out.Bytes(), nil
	}

	indent := lineIndent(doc, last.keyStart)
	unit := strings.TrimPrefix(indent, lineIndent(doc, obj.start))
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, value, indent, unit); err != nil {
		return nil, err
	}
	out.Write(doc[:last.value.end])
	out.WriteString(",\n" + indent)
	out.Write(key)
	out.WriteString(": ")
	out.Write(formatted.Bytes())
	out.Write(doc[last.value.end:])
	return out.Bytes(), nil
}

// multiline reports whether the members of a non-empty object start on lines
// of their own.
func multiline(doc []byte, obj node) bool {
	return bytes.Contains(doc[obj.start:obj.members[0].keyStart], []byte("\n"))
}

// splice returns doc with [from, to) replaced by text.
func splice(doc []byte, from, to int, text []byte) []byte {
	out := make([]byte, 0, len(doc)-(to-from)+len(text))
	out = append(out, doc[:from]...)
	out = append(out, text...)
	return append(out, doc[to:]...)
}

// docUnit is the indentation step of the document: the white space before its
// first indented line, or two spaces when it has none.
func docUnit(doc []byte) string {
	for _, line := range bytes.Split(doc, []byte("\n")) {
		trimmed := bytes.TrimLeft(line, " \t")
		if indent := line[:len(line)-len(trimmed)]; len(indent) > 0 && len(trimmed) > 0 {
			return string(indent)
		}
	}
	return "  "
}

// Delete removes the object member at path together with the comma that
// belongs to it. A path that is not there is not an error.
func Delete(doc []byte, path []string) ([]byte, error) {
	root, err := parse(doc)
	if err != nil {
		return nil, err
	}
	cur := root
	var parent node
	idx := 0
	for i, key := range path {
		j := slices.IndexFunc(cur.members, func(m member) bool { return m.key == key })
		if j < 0 || !cur.members[j].value.isObject && i < len(path)-1 {
			return doc, nil
		}
		parent, cur, idx = cur, cur.members[j].value, j
	}

	var from, to int
	switch {
	case idx > 0:
		from, to = parent.members[idx-1].value.end, parent.members[idx].value.end
	case len(parent.members) > 1:
		from, to = parent.members[0].keyStart, parent.members[1].keyStart
	default:
		from, to = parent.start+1, parent.end-1
	}
	return append(append([]byte{}, doc[:from]...), doc[to:]...), nil
}

func find(obj node, key string) *member {
	for i := range obj.members {
		if obj.members[i].key == key {
			return &obj.members[i]
		}
	}
	return nil
}

// array returns the array at path. found is false when the path is not there;
// it is an error when a value is there that is not an array.
func array(doc []byte, path []string) (root, arr node, found bool, err error) {
	root, err = parse(doc)
	if err != nil {
		return node{}, node{}, false, err
	}
	cur := root
	for i, key := range path {
		next := find(cur, key)
		if next == nil || !next.value.isObject && i < len(path)-1 {
			return root, node{}, false, nil
		}
		cur = next.value
	}
	if !cur.isArray {
		return node{}, node{}, false, fmt.Errorf("%q is not an array", strings.Join(path, "."))
	}
	return root, cur, true, nil
}

// Elements returns the elements of the array at path, exactly as the document
// has them. A path that is not there has none.
func Elements(doc []byte, path []string) (raws [][]byte, found bool, err error) {
	_, arr, found, err := array(doc, path)
	if err != nil || !found {
		return nil, false, err
	}
	for _, e := range arr.elems {
		raws = append(raws, doc[e.start:e.end])
	}
	return raws, true, nil
}

// multilineArray reports whether the elements of a non-empty array start on
// lines of their own.
func multilineArray(doc []byte, arr node) bool {
	return bytes.Contains(doc[arr.start:arr.elems[0].start], []byte("\n"))
}

// SetElement replaces the element of the array at path with value (compact
// JSON).
func SetElement(doc []byte, path []string, index int, value []byte) ([]byte, error) {
	_, arr, found, err := array(doc, path)
	if err != nil {
		return nil, err
	}
	if !found || index < 0 || index >= len(arr.elems) {
		return nil, fmt.Errorf("%q has no element %d", strings.Join(path, "."), index)
	}
	elem := arr.elems[index]
	if !multilineArray(doc, arr) {
		return splice(doc, elem.start, elem.end, value), nil
	}
	indent := lineIndent(doc, elem.start)
	unit := strings.TrimPrefix(indent, lineIndent(doc, arr.start))
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, value, indent, unit); err != nil {
		return nil, err
	}
	return splice(doc, elem.start, elem.end, formatted.Bytes()), nil
}

// AppendElement adds value (compact JSON) as the last element of the array at
// path, written the way the array's other elements are. An array that is not
// there is created, with the objects on the path.
func AppendElement(doc []byte, path []string, value []byte) ([]byte, error) {
	_, arr, found, err := array(doc, path)
	if err != nil {
		return nil, err
	}
	if !found {
		return Set(doc, path, append(append([]byte("["), value...), ']'))
	}

	if len(arr.elems) == 0 {
		// An empty array is opened up, one element per line.
		outer := lineIndent(doc, arr.start)
		indent := outer + docUnit(doc)
		var formatted bytes.Buffer
		if err := json.Indent(&formatted, value, indent, docUnit(doc)); err != nil {
			return nil, err
		}
		var out bytes.Buffer
		out.Write(doc[:arr.start+1])
		out.WriteString("\n" + indent)
		out.Write(formatted.Bytes())
		out.WriteString("\n" + outer)
		out.Write(doc[arr.end-1:])
		return out.Bytes(), nil
	}

	last := arr.elems[len(arr.elems)-1]
	if !multilineArray(doc, arr) {
		// Written like the other elements; with only one, like the brackets.
		sep := ","
		if len(arr.elems) > 1 {
			sep = string(doc[arr.elems[0].end:arr.elems[1].start])
		} else if arr.elems[0].start > arr.start+1 {
			sep = ", "
		}
		return splice(doc, last.end, last.end, append([]byte(sep), value...)), nil
	}
	indent := lineIndent(doc, last.start)
	unit := strings.TrimPrefix(indent, lineIndent(doc, arr.start))
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, value, indent, unit); err != nil {
		return nil, err
	}
	return splice(doc, last.end, last.end, append([]byte(",\n"+indent), formatted.Bytes()...)), nil
}

// DeleteElement removes the element of the array at path together with the
// comma that belongs to it. An array left without elements stays, as [].
func DeleteElement(doc []byte, path []string, index int) ([]byte, error) {
	_, arr, found, err := array(doc, path)
	if err != nil {
		return nil, err
	}
	if !found || index < 0 || index >= len(arr.elems) {
		return doc, nil
	}
	var from, to int
	switch {
	case index > 0:
		from, to = arr.elems[index-1].end, arr.elems[index].end
	case len(arr.elems) > 1:
		from, to = arr.elems[0].start, arr.elems[1].start
	default:
		from, to = arr.start+1, arr.end-1
	}
	return splice(doc, from, to, nil), nil
}
