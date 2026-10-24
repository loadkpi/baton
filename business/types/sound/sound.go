// Package sound defines the sound `baton watch` plays with a desktop
// notification: a name in the freedesktop sound theme, or the path of a file.
package sound

import (
	"errors"
	"regexp"
	"strings"
)

// namePattern is the alphabet of the names in the theme, and no more.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// maxNameLength bounds a name, so that a mistake in the config cannot make a
// huge file name.
const maxNameLength = 64

// Sound is a validated sound. The zero value means no sound.
type Sound struct {
	value string
}

// Parse validates a sound. The empty string is not a sound, but it is not an
// error either: it is the zero Sound. A value that starts with "/" or "~/" is
// a path, anything else must be a name. Whether the file exists is not asked
// here. The error does not repeat the value.
func Parse(s string) (Sound, error) {
	if s == "" {
		return Sound{}, nil
	}
	if IsPath(s) {
		if strings.ContainsAny(s, "\x00\n\r") {
			return Sound{}, errors.New("a sound path must not contain a NUL or a line break")
		}
		return Sound{value: s}, nil
	}
	if len(s) > maxNameLength || !namePattern.MatchString(s) {
		return Sound{}, errors.New("a sound is a name from the sound theme (letters, digits, '.', '_' and '-', at most 64 characters) or an absolute path (/... or ~/...)")
	}
	return Sound{value: s}, nil
}

// IsPath tells a path (absolute, or relative to the home directory with a
// leading "~/") from a name.
func IsPath(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~/")
}

// String returns the sound as written; empty for no sound.
func (s Sound) String() string {
	return s.value
}
