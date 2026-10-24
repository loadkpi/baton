package handofffile

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/foundation/atomicfile"
	"github.com/loadkpi/baton/foundation/filelock"
)

// ScrubTexts goes over every *.md file (the Handoffs and the draft) of every
// Task of every Project, under the lock the writers of the Task take.
func (s *Store) ScrubTexts(_ context.Context, mask func(text string) string, dryRun bool) ([]sessionbus.Scrubbed, error) {
	taskDirs, err := filepath.Glob(filepath.Join(s.dir, "*", "handoffs", "*"))
	if err != nil {
		return nil, err
	}
	var done []sessionbus.Scrubbed
	for _, dir := range taskDirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.md"))
		if err != nil {
			return done, err
		}
		for _, path := range files {
			lines, err := scrubText(path, mask, dryRun)
			if lines > 0 || err != nil {
				done = append(done, sessionbus.Scrubbed{Path: path, Lines: lines, Err: err})
			}
		}
	}
	return done, nil
}

// scrubText rewrites one file if masking changes it, and returns the number of
// lines of the new text that the old one did not have.
func scrubText(path string, mask func(string) string, dryRun bool) (int, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return 0, nil // gone since the listing, or not a file
	}
	if !dryRun {
		// The lock of the Task's writers: a Handoff or a draft being saved is
		// either whole before the look or whole after the rename.
		unlock, err := filelock.Acquire(filepath.Join(filepath.Dir(path), lockName))
		if err != nil {
			return 0, err
		}
		defer unlock()
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	masked := mask(string(data))
	if masked == string(data) {
		return 0, nil
	}

	had := make(map[string]bool)
	for _, l := range strings.Split(string(data), "\n") {
		had[l] = true
	}
	lines := 0
	for _, l := range strings.Split(masked, "\n") {
		if !had[l] {
			lines++
		}
	}
	lines = max(lines, 1)
	if dryRun {
		return lines, nil
	}
	if err := atomicfile.Write(path, []byte(masked), info.Mode().Perm()); err != nil {
		return 0, err
	}
	return lines, nil
}
