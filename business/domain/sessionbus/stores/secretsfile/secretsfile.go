// Package secretsfile is the secrets file (~/.config/baton/secrets.env) as the
// Session domain reads it: the values to mask.
package secretsfile

import (
	"context"
	"sort"

	"github.com/loadkpi/baton/foundation/secretsenv"
)

// Store reads the secrets file.
type Store struct {
	path string
}

// New creates the storage of the secrets file at path.
func New(path string) *Store {
	return &Store{path: path}
}

// QuerySecretValues returns the values of the file, sorted. A missing file has
// none. The error of a file that is refused or malformed names the file and a
// line number, and quotes nothing.
func (s *Store) QuerySecretValues(_ context.Context) ([]string, error) {
	vars, err := secretsenv.Read(s.path)
	if err != nil {
		return nil, err
	}
	values := make([]string, 0, len(vars))
	for _, v := range vars {
		values = append(values, v)
	}
	sort.Strings(values)
	return values, nil
}
