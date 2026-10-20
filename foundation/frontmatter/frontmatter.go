// Package frontmatter reads one list-valued key from the YAML front matter of
// a Markdown file, without a YAML library.
//
// Front matter is the part between a first line "---" and the next line
// "---". Only a top-level key (a line that starts in column 0) is read, so
// text inside a multi-line value is never mistaken for a key.
package frontmatter

import (
	"errors"
	"fmt"
	"strings"
)

// List returns the values of the top-level key in the front matter of text.
// found is false when there is no front matter or the key is absent. The key
// may be a flow list ("key: [a, b]"), a single value ("key: a") or a block
// list ("key:" followed by "- a" lines). An empty list is an error: it is
// ambiguous whether the author meant "no restriction" or "nowhere".
func List(text, key string) (values []string, found bool, err error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != "---" {
		return nil, false, nil
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, false, errors.New("front matter is not closed by a line \"---\"")
	}
	fm := lines[1:end]

	for i, line := range fm {
		rest, ok := strings.CutPrefix(line, key+":")
		if !ok {
			continue
		}
		if found {
			return nil, false, fmt.Errorf("key %q appears twice", key)
		}
		found = true
		values, err = parseValue(rest, fm[i+1:], key)
		if err != nil {
			return nil, false, err
		}
	}
	return values, found, nil
}

// parseValue reads the value after "key:" (rest) and, for a block list, the
// lines that follow it.
func parseValue(rest string, following []string, key string) ([]string, error) {
	rest = strings.TrimSpace(stripComment(rest))
	switch {
	case rest == "":
		var values []string
		for _, line := range following {
			t := strings.TrimSpace(line)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			item, ok := strings.CutPrefix(t, "-")
			if !ok || (item != "" && item[0] != ' ') {
				break
			}
			values = append(values, unquote(strings.TrimSpace(stripComment(item))))
		}
		return nonEmpty(values, key)
	case strings.HasPrefix(rest, "["):
		inner, ok := strings.CutSuffix(rest, "]")
		if !ok {
			return nil, fmt.Errorf("%s: the list is not closed by \"]\" on the same line", key)
		}
		var values []string
		for _, item := range strings.Split(inner[1:], ",") {
			if item = strings.TrimSpace(item); item != "" {
				values = append(values, unquote(item))
			}
		}
		return nonEmpty(values, key)
	default:
		return nonEmpty([]string{unquote(rest)}, key)
	}
}

func nonEmpty(values []string, key string) ([]string, error) {
	for _, v := range values {
		if v == "" {
			return nil, fmt.Errorf("%s: an empty value", key)
		}
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("%s: the list is empty", key)
	}
	return values, nil
}

// stripComment removes a trailing " # comment".
func stripComment(s string) string {
	if i := strings.Index(s, " #"); i >= 0 {
		return s[:i]
	}
	return s
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}
