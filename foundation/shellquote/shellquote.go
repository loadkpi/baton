// Package shellquote writes a command line that a POSIX shell reads back as
// the same words.
package shellquote

import "strings"

// Join returns the words as one command line: a word made only of characters
// that no shell treats specially stays as it is, every other word is wrapped
// in single quotes.
func Join(words []string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = quote(w)
	}
	return strings.Join(quoted, " ")
}

func quote(w string) string {
	if w != "" && !strings.ContainsFunc(w, needsQuoting) {
		return w
	}
	return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
}

// needsQuoting is true for anything outside the characters that are plain in
// every shell: letters, digits and the punctuation of paths and assignments.
func needsQuoting(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	}
	return !strings.ContainsRune("_@%+=:,./-", r)
}
