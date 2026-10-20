// Package tag defines a tag: a label on a source item (a skill, a part of the
// instructions) that a Profile can accept.
package tag

import (
	"fmt"
	"regexp"
	"slices"
)

// pattern is a lowercase word. It is the same alphabet as a Profile name, so
// tags are safe to write in the config, in YAML and in Markdown comments.
var pattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Tag is a validated tag such as "work".
type Tag struct {
	value string
}

// Parse validates a tag.
func Parse(s string) (Tag, error) {
	if !pattern.MatchString(s) {
		return Tag{}, fmt.Errorf("invalid tag %q: use lowercase letters, digits, '.', '_' and '-', starting with a letter or digit", s)
	}
	return Tag{value: s}, nil
}

// ParseAll validates every tag of a list.
func ParseAll(list []string) ([]Tag, error) {
	tags := make([]Tag, 0, len(list))
	for _, s := range list {
		t, err := Parse(s)
		if err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, nil
}

// String returns the tag as written.
func (t Tag) String() string {
	return t.value
}

// AcceptedBy reports whether an item with these tags reaches a Profile that
// accepts the given tags. An untagged item reaches every Profile; a tagged one
// reaches a Profile that accepts at least one of its tags.
func AcceptedBy(itemTags, accepted []Tag) bool {
	if len(itemTags) == 0 {
		return true
	}
	return slices.ContainsFunc(itemTags, func(t Tag) bool { return slices.Contains(accepted, t) })
}
