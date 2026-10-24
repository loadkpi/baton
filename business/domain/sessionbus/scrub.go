package sessionbus

import (
	"context"
)

// Scrubbed is what Scrub did, or would do, to one file.
type Scrubbed struct {
	// Path is the file on the host.
	Path string
	// Lines is how many lines of it changed: for an Event file the Events
	// whose payload was masked, for a text the lines of the new text that the
	// old one did not have.
	Lines int
	// Err is why the file could not be rewritten. It is then as it was.
	Err error
}

// Scrub applies the masking of the hook to what is already on disk: the
// payload of every Event of every Event file, active Sessions included, and the
// text of every Handoff and draft. It uses the same code and the same
// secrets as the hook, so a secret the hook masks is masked here, and a file
// that holds none is not written. A line that is not an Event stays as it is.
// With dryRun nothing is written and the result is what would be.
//
// A file that cannot be rewritten does not stop the others: it is returned
// with its Err. The files that changed come first, Event files then texts.
func (b *Bus) Scrub(ctx context.Context, dryRun bool) ([]Scrubbed, error) {
	r := b.redactor(ctx)
	refs, err := b.storer.QuerySessions(ctx)
	if err != nil {
		return nil, err
	}

	var done []Scrubbed
	for _, ref := range refs {
		s := Scrubbed{Path: b.storer.QueryPath(ref)}
		s.Lines, s.Err = b.storer.ScrubSession(ctx, ref, func(payload []byte) ([]byte, bool) {
			return maskPayloadWith(r, payload)
		}, dryRun)
		if s.Lines > 0 || s.Err != nil {
			done = append(done, s)
		}
	}

	if b.texts != nil {
		texts, err := b.texts.ScrubTexts(ctx, r.Mask, dryRun)
		done = append(done, texts...)
		if err != nil {
			return done, err
		}
	}
	return done, nil
}
