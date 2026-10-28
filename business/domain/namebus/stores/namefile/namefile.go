// Package namefile reads the names Agents keep in their own files.
package namefile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"

	"github.com/loadkpi/baton/business/domain/namebus"
)

const (
	// chunkSize is how much of a file is read at a time, from its end.
	chunkSize = 64 << 10
	// maxRead is how much of a file is read in all before giving up: a
	// transcript can be several MB, and the title is repeated in it.
	maxRead = 4 << 20
)

// Store reads files; it keeps nothing.
type Store struct {
	// chunk and limit are chunkSize and maxRead.
	chunk, limit int
}

// New creates the store.
func New() *Store { return &Store{chunk: chunkSize, limit: maxRead} }

// QueryStamp returns the size and modification time of the file.
func (s *Store) QueryStamp(_ context.Context, path string) (namebus.Stamp, bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return namebus.Stamp{}, false, nil
	}
	if err != nil {
		return namebus.Stamp{}, false, err
	}
	return namebus.Stamp{Size: info.Size(), ModTime: info.ModTime()}, true, nil
}

// QueryClaudeTitle returns the newest line of a Claude Code transcript that is
// a title: {"type":"ai-title","aiTitle":"..."} or, for a name the user set,
// {"type":"custom-title","customTitle":"..."} [unverified].
func (s *Store) QueryClaudeTitle(ctx context.Context, path string) (string, bool, error) {
	var title string
	err := s.scanBackward(ctx, path, func(line []byte) bool {
		if !bytes.Contains(line, []byte("-title")) {
			return true
		}
		var doc struct {
			Type        string `json:"type"`
			AITitle     string `json:"aiTitle"`
			CustomTitle string `json:"customTitle"`
		}
		if json.Unmarshal(line, &doc) != nil {
			return true
		}
		switch {
		case doc.Type == "custom-title" && doc.CustomTitle != "":
			title = doc.CustomTitle
		case doc.Type == "ai-title" && doc.AITitle != "":
			title = doc.AITitle
		}
		return title == ""
	})
	return title, title != "", err
}

// QueryCodexName returns the newest line of a Codex session index for the
// thread: {"id":"...","thread_name":"...","updated_at":"..."}.
func (s *Store) QueryCodexName(ctx context.Context, path, threadID string) (string, bool, error) {
	var name string
	err := s.scanBackward(ctx, path, func(line []byte) bool {
		if !bytes.Contains(line, []byte(threadID)) {
			return true
		}
		var doc struct {
			ID   string `json:"id"`
			Name string `json:"thread_name"`
		}
		if json.Unmarshal(line, &doc) != nil || doc.ID != threadID || doc.Name == "" {
			return true
		}
		name = doc.Name
		return false
	})
	return name, name != "", err
}

// scanBackward calls yield with the complete lines of the file, the last one
// first, until it returns false or the limit is read. A missing file has no
// lines. Only a chunk and one line are in memory at a time.
func (s *Store) scanBackward(ctx context.Context, path string, yield func(line []byte) bool) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}

	end := info.Size()
	stop := max(end-int64(s.limit), 0)
	// tail is the start of a line whose beginning is not read yet.
	var tail []byte
	buf := make([]byte, s.chunk)
	for end > stop {
		if err := ctx.Err(); err != nil {
			return err
		}
		start := max(end-int64(s.chunk), stop)
		chunk := buf[:end-start]
		if _, err := f.ReadAt(chunk, start); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		end = start

		data := append(append([]byte(nil), chunk...), tail...)
		// Everything after the first newline is complete lines; what is
		// before it may be the end of a line that began in an earlier chunk.
		first := bytes.IndexByte(data, '\n')
		if first < 0 {
			tail = data
			continue
		}
		tail = data[:first]
		rest := data[first+1:]
		for len(rest) > 0 {
			i := bytes.LastIndexByte(bytes.TrimRight(rest, "\n"), '\n')
			line := bytes.TrimRight(rest[i+1:], "\n")
			rest = rest[:i+1]
			if len(line) > 0 && !yield(line) {
				return nil
			}
		}
	}
	if stop == 0 && len(tail) > 0 { // the first line of the file
		yield(bytes.TrimRight(tail, "\n"))
	}
	return nil
}
