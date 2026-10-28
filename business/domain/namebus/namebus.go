// Package namebus finds the name an Agent gave to a Session, to tell Sessions
// apart without opening their tabs.
//
// This is the one place where Baton reads an Agent's own session files
// (DESIGN.md, ticket 32): read-only, one field, and with a fallback, the first
// prompt of the Session from Baton's own Event file. Nothing is written.
package namebus

import (
	"context"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/loadkpi/baton/business/domain/runtimebus"
	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/runtime"
)

// maxName is how many characters of a name are kept.
const maxName = 120

// codexIndexInSandbox is where a Codex inside an ai-sandbox container keeps the
// index of its threads (CODEX_HOME is ~/.codex there, DESIGN.md, ticket 16).
const codexIndexInSandbox = "/home/sandbox/.codex/session_index.jsonl"

// Stamp says whether a file changed: its size and modification time.
type Stamp struct {
	Size    int64
	ModTime time.Time
}

func (s Stamp) same(o Stamp) bool { return s.Size == o.Size && s.ModTime.Equal(o.ModTime) }

// Storer reads the Agents' files. A file that is not there is not an error:
// ok is false.
type Storer interface {
	// QueryStamp returns the size and modification time of the file.
	QueryStamp(ctx context.Context, path string) (s Stamp, ok bool, err error)
	// QueryClaudeTitle returns the newest title (`custom-title` or `ai-title`)
	// of a Claude Code transcript, reading it from its end and giving up
	// after a bounded amount.
	QueryClaudeTitle(ctx context.Context, path string) (title string, ok bool, err error)
	// QueryCodexName returns the newest `thread_name` of the thread in a
	// Codex `session_index.jsonl`, read the same way.
	QueryCodexName(ctx context.Context, path, threadID string) (name string, ok bool, err error)
}

// Prompts is where the fallback comes from: the Session's first prompt.
type Prompts interface {
	FirstPrompt(ctx context.Context, s sessionbus.Session) (prompt string, ok bool, err error)
}

// Bus is the name domain. It remembers the names it found, so that a screen
// that asks again and again reads a file only when it changed.
type Bus struct {
	storer  Storer
	prompts Prompts

	mu       sync.Mutex
	runtimes map[runtime.Runtime]runtimebus.Runtime
	cache    map[sessionbus.Key]entry
}

// entry is what is known of a Session's name; stamp is of the file it came
// from, and a name found in a file is valid while the stamp is the same.
type entry struct {
	path  string
	stamp Stamp
	name  string // empty: the file has no name for the Session
	// prompt is the fallback, which never changes once found.
	prompt      string
	promptFound bool
}

// New creates the domain over the Agents' files and the Event files.
func New(storer Storer, prompts Prompts) *Bus {
	return &Bus{storer: storer, prompts: prompts, cache: make(map[sessionbus.Key]entry)}
}

// Configure says how paths inside the Runtimes are mapped to the host. It can
// be called again when the configuration is read anew; what was found stays
// cached.
func (b *Bus) Configure(runtimes map[runtime.Runtime]runtimebus.Runtime) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.runtimes = runtimes
}

// Name is the name of the Session: the one its Agent gave it, else its first
// prompt, else "". Whatever cannot be read is taken for not there.
func (b *Bus) Name(ctx context.Context, s sessionbus.Session) string {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := s.Key()
	e := b.cache[key]
	if file := b.nativeFile(ctx, s); file != "" {
		stamp, ok, err := b.storer.QueryStamp(ctx, file)
		if err == nil && ok && (e.path != file || !e.stamp.same(stamp)) {
			e.path, e.stamp, e.name = file, stamp, b.native(ctx, s, file)
		}
	}
	if e.name == "" && !e.promptFound {
		if p, ok, err := b.prompts.FirstPrompt(ctx, s); err == nil && ok {
			e.prompt, e.promptFound = p, true
		}
	}
	b.cache[key] = e
	if e.name != "" {
		return e.name
	}
	return e.prompt
}

// native reads the name from the Agent's file.
func (b *Bus) native(ctx context.Context, s sessionbus.Session, file string) string {
	var name string
	var ok bool
	var err error
	switch s.Agent {
	case agent.Claude:
		name, ok, err = b.storer.QueryClaudeTitle(ctx, file)
	case agent.Codex:
		name, ok, err = b.storer.QueryCodexName(ctx, file, s.SessionID)
	}
	if err != nil || !ok {
		return ""
	}
	return sessionbus.OneLine(name, maxName)
}

// nativeFile is the host path of the file that holds the Session's name, or ""
// when there is none Baton may read: a Compose Session's files are inside the
// container or a volume, and a sandbox path is mapped the way the sandbox
// Runtime maps every path.
func (b *Bus) nativeFile(ctx context.Context, s sessionbus.Session) string {
	switch s.Runtime {
	case runtime.Host:
		switch s.Agent {
		case agent.Claude:
			if filepath.IsAbs(s.Transcript) {
				return s.Transcript
			}
		}
		// A Codex on the host has no Profile in the configuration (Codex runs
		// only in a sandbox, DESIGN.md, ticket 12), so there is no index to
		// look in.
	case runtime.Sandbox:
		inside := s.Transcript
		if s.Agent == agent.Codex {
			inside = codexIndexInSandbox
		}
		rt, ok := b.runtimes[runtime.Sandbox]
		if !ok || !path.IsAbs(inside) {
			return ""
		}
		host, err := rt.HostPath(ctx, s.Project, inside)
		if err != nil {
			return ""
		}
		return host
	}
	return ""
}
