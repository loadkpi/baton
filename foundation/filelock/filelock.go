// Package filelock takes advisory exclusive locks on files (Linux).
package filelock

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// LockFile blocks until it holds an exclusive lock on f. Closing f releases the
// lock, and so does the end of the process, however it ends.
func LockFile(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return fmt.Errorf("lock %s: %w", f.Name(), err)
		}
		return nil
	}
}

// Acquire locks the file at path, creating it (and the directories above it,
// 0700) when needed, and returns the function that releases the lock. The file
// only carries the lock and is never written.
func Acquire(path string) (release func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := LockFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}
