package sessionbus

import (
	"context"
	"errors"
	"io/fs"
	"time"
)

// Pruned is what Prune did, or would do, to the Event file of one Session.
type Pruned struct {
	Session Session
	// Path is the Event file on the host.
	Path string
	// Size is the size of the file in bytes.
	Size int64
	// Err is why the file could not be deleted, or its size not be read. The
	// file is then still there.
	Err error
}

// Prune deletes the Event files of the Sessions that are over (ended or lost)
// and whose newest Event, the Agent's or Baton's, is older than olderThan.
// sessions are the Sessions as List returned them, which is what decides
// whether a Session is over: a Session that is not over is never touched, and
// neither is anything but the Event file (Handoffs, Tasks and the rest of a
// Project's directory stay). With dryRun nothing is deleted and the result is
// what would be.
//
// The decision is made again for each file under the lock a hook takes to
// append to it, so a Session whose Agent wrote an Event after List (it came
// back) keeps its file, and a hook that waits for the lock when the file goes
// writes to a new file instead (jsonl.Append). A file that cannot be deleted
// does not stop the others: it is returned with its Err. A file that was kept
// because of that second look is not returned.
//
// A launch record that the Events of the pruned Sessions had hidden (some of
// them were its only Events at or after it) would show as silent in `baton ls`
// and tell the user to run /hooks for an Agent that worked, so those records
// are deleted too and returned. A launch that was silent before stays. With
// dryRun no record is deleted or returned.
func (b *Bus) Prune(ctx context.Context, sessions []Session, olderThan time.Duration, dryRun bool) ([]Pruned, []Launch, error) {
	cutoff := b.now().Add(-olderThan)
	old := func(s Session) (bool, error) {
		newest, ok, err := b.newestEvent(ctx, s.Ref)
		return ok && newest.Before(cutoff), err
	}

	var pruned []Pruned
	for _, s := range sessions {
		if s.Active() {
			continue
		}
		if ok, err := old(s); err != nil {
			return nil, nil, err
		} else if !ok {
			continue
		}

		p := Pruned{Session: s, Path: b.storer.QueryPath(s.Ref)}
		if dryRun {
			p.Size, p.Err = b.storer.QuerySize(ctx, s.Ref)
			pruned = append(pruned, p)
			continue
		}
		size, deleted, err := b.storer.DeleteSession(ctx, s.Ref, func() (bool, error) { return old(s) })
		switch {
		case err != nil:
			p.Err = err
		case !deleted:
			continue
		}
		p.Size = size
		pruned = append(pruned, p)
	}
	if dryRun {
		return pruned, nil, nil
	}
	removed, err := b.pruneLaunches(ctx, sessions, pruned)
	return pruned, removed, err
}

// pruneLaunches deletes the launch records that were heard from before the
// prune and are not after it, judged by the Sessions whose files are still
// there.
func (b *Bus) pruneLaunches(ctx context.Context, sessions []Session, pruned []Pruned) ([]Launch, error) {
	if len(pruned) == 0 {
		return nil, nil
	}
	gone := make(map[Key]bool)
	for _, p := range pruned {
		if p.Err == nil {
			gone[p.Session.Key()] = true
		}
	}
	var left []Session
	for _, s := range sessions {
		if !gone[s.Key()] {
			left = append(left, s)
		}
	}
	launches, err := b.storer.QueryLaunches(ctx)
	if err != nil {
		return nil, err
	}
	var removed []Launch
	for _, l := range launches {
		if !heardFrom(sessions, l) || heardFrom(left, l) {
			continue
		}
		deleted, err := b.storer.DeleteLaunch(ctx, l)
		if err != nil {
			return removed, err
		}
		if deleted {
			removed = append(removed, l)
		}
	}
	return removed, nil
}

// newestEvent is the time of the newest Event of the file, whoever wrote it;
// ok is false when the file holds none.
func (b *Bus) newestEvent(ctx context.Context, ref SessionRef) (ts time.Time, ok bool, err error) {
	err = b.storer.QueryEventsBackward(ctx, ref, func(e Event) bool {
		ts, ok = e.TS, true
		return false
	})
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, false, nil // gone since List: another prune
	}
	return ts, ok, err
}
