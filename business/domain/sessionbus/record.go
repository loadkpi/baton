package sessionbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/runtime"
)

// maxAncestors bounds the walk from the hook up to the Agent. An Agent starts
// a hook through at most a shell or two.
const maxAncestors = 8

// Hook is one call of `baton hook`.
type Hook struct {
	Agent   agent.Agent
	Profile profilename.Name
	Runtime runtime.Runtime
	// ParentPID is the parent of the hook process.
	ParentPID int
	// Payload is what the Agent wrote to the hook's standard input.
	Payload []byte
}

// sessionIDPattern is what a session id may look like, because it becomes part
// of a file name. Claude Code uses UUIDs.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Record appends the Event of a hook call to the file of its Session, with the
// secrets of its payload masked.
func (b *Bus) Record(ctx context.Context, h Hook) error {
	var fields struct {
		SessionID string `json:"session_id"`
		Event     string `json:"hook_event_name"`
		CWD       string `json:"cwd"`
	}
	if err := json.Unmarshal(h.Payload, &fields); err != nil {
		return fmt.Errorf("payload is not a JSON object: %w", err)
	}
	if !sessionIDPattern.MatchString(fields.SessionID) {
		return errors.New("payload has no usable session_id")
	}
	if fields.Event == "" {
		return errors.New("payload has no hook_event_name")
	}

	payload, masked := b.maskPayload(ctx, h.Payload)

	host, err := b.machine.QueryHostname(ctx)
	if err != nil {
		return err
	}
	proc, fallback := b.findAgent(ctx, h.Agent, h.ParentPID)
	// Best effort: the id lets a reader find the container whatever its host
	// name is, and nothing else depends on it, so a hook never fails for it.
	var container string
	if h.Runtime != runtime.Host {
		container, _ = b.machine.QueryContainer(ctx)
	}

	return b.storer.SaveEvent(ctx, Event{
		TS:               b.now().UTC(),
		Agent:            h.Agent,
		Profile:          h.Profile,
		Runtime:          h.Runtime,
		Host:             host,
		AgentPID:         proc.PID,
		AgentStart:       proc.Start,
		AgentPIDFallback: fallback,
		Container:        container,
		SessionID:        fields.SessionID,
		Name:             fields.Event,
		CWD:              fields.CWD,
		Payload:          payload,
		Masked:           masked,
	})
}

// findAgent walks up from the hook's parent to the nearest ancestor that is
// the Agent's process, for at most maxAncestors steps. Whether the Agent starts
// a hook directly or through `sh -c` is [unverified] (DESIGN.md), so the first
// process is not assumed to be the Agent. When no ancestor is, the direct
// parent is the answer and fallback is true. A process that cannot be read is
// reported by its PID alone, with a start time of 0.
func (b *Bus) findAgent(ctx context.Context, a agent.Agent, parent int) (found Process, fallback bool) {
	direct := Process{PID: parent}
	pid := parent
	for step := 0; step < maxAncestors && pid > 1; step++ {
		p, err := b.machine.QueryProcess(ctx, pid)
		if err != nil {
			break
		}
		if step == 0 {
			direct = p
		}
		if p.Comm == a.Binary() || p.ExeBase == a.Binary() {
			return p, false
		}
		pid = p.PPID
	}
	return direct, true
}
