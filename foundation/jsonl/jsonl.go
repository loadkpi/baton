// Package jsonl appends to and reads files that hold one JSON document per
// line.
package jsonl

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/loadkpi/baton/foundation/filelock"
)

// Append adds line, followed by a newline, to the file at path under an
// exclusive lock on the file, and creates the file (with the directories
// above it, 0700) with permission bits perm when it does not exist.
//
// The line goes out in one write, and the lock makes parallel appenders take
// turns, so lines never interleave however large they are. A file that
// already exists keeps its permission bits.
func Append(path string, line []byte, perm os.FileMode) (err error) {
	if bytes.ContainsRune(line, '\n') {
		return errors.New("a line must not contain a newline")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := openLocked(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, perm)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr // closing also releases the lock
		}
	}()

	_, err = f.Write(append(line[:len(line):len(line)], '\n'))
	return err
}

// openLocked opens the file at path and takes the exclusive lock on it. A lock
// is on a file, not on a name: if Remove deleted the file while this process
// waited for the lock, the lock is on a file nobody can reach, and a line
// written to it would be lost. So the name is looked at again once the lock is
// held, and when it no longer leads to the file that was opened, the file is
// opened again (and created, when flag says so).
func openLocked(path string, flag int, perm os.FileMode) (*os.File, error) {
	for {
		f, err := os.OpenFile(path, flag, perm)
		if err != nil {
			return nil, err
		}
		if err := filelock.LockFile(f); err != nil {
			_ = f.Close()
			return nil, err
		}
		held, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		current, err := os.Stat(path)
		switch {
		case err == nil && os.SameFile(held, current):
			return f, nil
		case err == nil || errors.Is(err, fs.ErrNotExist):
			_ = f.Close() // replaced or deleted while we waited
		default:
			_ = f.Close()
			return nil, err
		}
	}
}

// Remove deletes the file at path, under the lock Append takes on it, so that
// a line being appended is either in the file when it goes or lands in a new
// file (Append opens the file again when it finds it gone). confirm runs with
// the lock held, after any appender that was ahead has finished; the file is
// deleted only when it returns true. Remove returns the size of the file it
// deleted. A file that is not there is not deleted and is no error.
func Remove(path string, confirm func() (bool, error)) (size int64, removed bool, err error) {
	f, err := openLocked(path, os.O_RDONLY, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = f.Close() }()

	if ok, err := confirm(); err != nil || !ok {
		return 0, false, err
	}
	info, err := f.Stat()
	if err != nil {
		return 0, false, err
	}
	if err := os.Remove(path); err != nil {
		return 0, false, err
	}
	return info.Size(), true, nil
}

// Forward calls yield with each complete line of the file at path, oldest
// first, until yield returns false. Bytes after the last newline are an
// unfinished line and are not passed on. The slice is valid until yield
// returns.
func Forward(path string, yield func(line []byte) bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if err == io.EOF {
			return nil // what is left has no newline: not complete yet
		}
		if err != nil {
			return err
		}
		if !yield(line[:len(line)-1]) {
			return nil
		}
	}
}

// chunkSize is how much Backward reads at a time.
const chunkSize = 64 << 10

// Backward calls yield with each complete line of the file at path, newest
// first, until yield returns false. It reads from the end of the file, so
// finding the last line of a large file costs one chunk, not the file. Bytes
// after the last newline are an unfinished line (an appender is mid-write) and
// are not passed on. The slice is valid until yield returns.
func Backward(path string, yield func(line []byte) bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}

	end, err := afterLastNewline(f, info.Size())
	if err != nil || end == 0 {
		return err
	}

	// pending is the tail of the line that has been read in part: the part
	// to the right of the chunk being looked at.
	var pending []byte
	pos := end - 1 // the newline that ends the last line is not part of it
	for pos > 0 {
		n := min(int64(chunkSize), pos)
		pos -= n
		buf := make([]byte, n)
		if _, err := f.ReadAt(buf, pos); err != nil {
			return err
		}
		data := append(buf, pending...)
		for {
			i := bytes.LastIndexByte(data, '\n')
			if i < 0 {
				break
			}
			if !yield(data[i+1:]) {
				return nil
			}
			data = data[:i]
		}
		pending = data
	}
	yield(pending)
	return nil
}

// afterLastNewline is the offset just after the last newline of the first size
// bytes of f, or 0 if there is none.
func afterLastNewline(f *os.File, size int64) (int64, error) {
	for pos := size; pos > 0; {
		n := min(int64(chunkSize), pos)
		pos -= n
		buf := make([]byte, n)
		if _, err := f.ReadAt(buf, pos); err != nil {
			return 0, err
		}
		if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
			return pos + int64(i) + 1, nil
		}
	}
	return 0, nil
}
