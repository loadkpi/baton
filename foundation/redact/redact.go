// Package redact replaces secrets in text with a mask: literal values the
// caller knows, and what looks like a secret whoever wrote it (private key
// blocks, bearer tokens, well-known token shapes, the value of `NAME=value`
// where the name says secret).
//
// It is a safety net, not a guarantee: a secret that has none of these shapes
// and is not among the literals stays.
package redact

import (
	"regexp"
	"sort"
	"strings"
)

// Mask is what replaces a secret.
const Mask = "[REDACTED]"

// MinLiteral is the length under which a literal is not masked: a short value
// would turn up in ordinary text and hide it.
const MinLiteral = 8

// Redactor masks secrets. The zero value masks by pattern only.
type Redactor struct {
	// literals are the values to mask, longest first, so that a value that
	// contains another one goes whole.
	literals []string
}

// New creates a Redactor for the literal secrets. Values shorter than
// MinLiteral characters are ignored.
func New(literals []string) *Redactor {
	seen := make(map[string]bool, len(literals))
	var kept []string
	for _, l := range literals {
		if len(l) >= MinLiteral && !seen[l] {
			seen[l] = true
			kept = append(kept, l)
		}
	}
	sort.Slice(kept, func(i, j int) bool {
		if len(kept[i]) != len(kept[j]) {
			return len(kept[i]) > len(kept[j])
		}
		return kept[i] < kept[j]
	})
	return &Redactor{literals: kept}
}

// whole are the patterns whose whole match is a secret.
var whole = []*regexp.Regexp{
	// A private key block, to the END line or, when the text is cut, to its end.
	// RE2 has no back-references, so the END line need not name the BEGIN's kind.
	regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|\z)`),
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]+`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`glpat-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`xox[abprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`),
	// A Telegram bot token follows "bot" in a URL, so no word boundary before it.
	regexp.MustCompile(`[0-9]{8,10}:[A-Za-z0-9_-]{35}`),
}

// bearer is the keyword and the token of an Authorization header; the keyword
// stays.
var bearer = regexp.MustCompile(`(?i)(\bbearer[ \t]+)[A-Za-z0-9._~+/=-]+`)

// assignment is `NAME=value`, `NAME: value` and `"NAME": "value"` where NAME
// has a word of a secret in it; submatch 1 is everything up to the value and
// submatch 2 the value, which goes.
var assignment = regexp.MustCompile(`(?i)(["']?[\w.-]*(?:token|secret|password|passwd|api[_-]?key|private[_-]key)[\w.-]*["']?[ \t]*[:=][ \t]*)("[^"]+"|'[^']+'|[^\s"',;&]+)`)

// secretKey is a word that makes a name, such as a JSON key, say that its value
// is a secret; the same words as in assignment.
var secretKey = regexp.MustCompile(`(?i)token|secret|password|passwd|api[_-]?key|private[_-]key`)

// SecretKey reports whether name, in any case, has a word of a secret in it. It
// is for the caller that walks structured data: the value under such a key can
// be masked whole, whatever it looks like.
func SecretKey(name string) bool {
	return secretKey.MatchString(name)
}

// Mask returns s with its secrets replaced by Mask. It is s itself when there
// are none.
func (r *Redactor) Mask(s string) string {
	for _, l := range r.literals {
		s = strings.ReplaceAll(s, l, Mask)
	}
	for _, p := range whole {
		s = p.ReplaceAllLiteralString(s, Mask)
	}
	s = bearer.ReplaceAllString(s, "${1}"+Mask)
	return maskAssignments(s)
}

// maskAssignments replaces the value of every assignment, and keeps its quotes.
func maskAssignments(s string) string {
	matches := assignment.FindAllStringSubmatchIndex(s, -1)
	if matches == nil {
		return s
	}
	var out strings.Builder
	last := 0
	for _, m := range matches {
		out.WriteString(s[last:m[2*2]]) // up to the value
		switch value := s[m[2*2]:m[2*2+1]]; value[0] {
		case '"':
			out.WriteString(`"` + Mask + `"`)
		case '\'':
			out.WriteString(`'` + Mask + `'`)
		default:
			out.WriteString(Mask)
		}
		last = m[2*2+1]
	}
	out.WriteString(s[last:])
	return out.String()
}
