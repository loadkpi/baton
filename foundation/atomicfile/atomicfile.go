// Package atomicfile writes files so that readers never see a partial file.
package atomicfile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// Write replaces the file at path with data and the given permission bits.
// The data goes to a temporary file in the same directory first and is then
// renamed over path, which is atomic on the same file system. Missing parent
// directories are created with mode 0755.
func Write(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// WriteIfChanged is Write that leaves the file alone when it already has
// exactly this content and these permission bits. It reports whether it wrote.
func WriteIfChanged(path string, data []byte, perm os.FileMode) (bool, error) {
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm() == perm {
		if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
			return false, nil
		}
	}
	return true, Write(path, data, perm)
}
