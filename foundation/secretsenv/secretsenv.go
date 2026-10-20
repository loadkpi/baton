// Package secretsenv reads a secrets file: `NAME=value` lines.
//
// Blank lines and lines starting with "#" are ignored. The value is everything
// after the first "=", trimmed, with one pair of matching single or double
// quotes removed (no escapes). Errors name the file and the line number and
// never quote a line, because a line may be a secret.
package secretsenv

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var variableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidName reports whether name can be the name of a variable.
func ValidName(name string) bool { return variableName.MatchString(name) }

// Read returns the variables of the file at path. A missing file holds no
// values. A file that any group or other permission bit lets others read is
// refused, because it holds secrets.
func Read(path string) (map[string]string, error) {
	values := map[string]string{}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s is accessible by others (mode %04o); run: chmod 600 %s", path, info.Mode().Perm(), path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || !ValidName(name) {
			// The line is not quoted: it may be a secret.
			return nil, fmt.Errorf("%s:%d: expected NAME=value", path, i+1)
		}
		values[name] = unquote(strings.TrimSpace(value))
	}
	return values, nil
}

// unquote removes one pair of matching single or double quotes around a value.
// There are no escapes: what is between the quotes is the value.
func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}
