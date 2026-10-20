package sessionbus

import (
	"context"
	"sort"
	"time"

	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/runtime"
)

// Launch is the record that Baton started an Agent in a Project's container. It
// is the only way Baton can know about a Session that has reported nothing: a
// Session exists for Baton through its Events, and a Codex that has not been
// trusted with /hooks sends none (DESIGN.md, D3).
type Launch struct {
	// TS is when the launch began, before the tab was opened, so that every
	// Event of the Session is at or after it.
	TS      time.Time
	Project projectname.Name
	Agent   agent.Agent
	Profile profilename.Name
	Runtime runtime.Runtime
	// Task is the label of the Task the Session was started for.
	Task string
	// Title is the title of the tab.
	Title string
}

// RecordLaunch remembers a launch. A Project has one record per Agent: the
// newest launch replaces the one before it.
func (b *Bus) RecordLaunch(ctx context.Context, l Launch) error {
	l.TS = l.TS.UTC()
	return b.storer.SaveLaunch(ctx, l)
}

// Silent returns the launches that no Event has followed yet, the most recent
// first. sessions are the Sessions as List returned them: an Event of the same
// Project and Agent at or after the launch, from any Session (a resumed one
// included), means the Agent's hooks work.
func (b *Bus) Silent(ctx context.Context, sessions []Session) ([]Launch, error) {
	launches, err := b.storer.QueryLaunches(ctx)
	if err != nil {
		return nil, err
	}
	var silent []Launch
	for _, l := range launches {
		if !heardFrom(sessions, l) {
			silent = append(silent, l)
		}
	}
	sort.SliceStable(silent, func(i, j int) bool { return silent[i].TS.After(silent[j].TS) })
	return silent, nil
}

func heardFrom(sessions []Session, l Launch) bool {
	for _, s := range sessions {
		if s.Project == l.Project && s.Agent == l.Agent && !s.Last.Before(l.TS) {
			return true
		}
	}
	return false
}
