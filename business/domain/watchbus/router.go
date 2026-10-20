package watchbus

import (
	"context"
	"errors"
	"fmt"
)

// Failure is an error of a Notifier that says what kind of failure it was, so
// that the Router warns once per kind and not once per message.
type Failure struct {
	// Kind is short and the same for every failure of the same sort, for
	// example "connection" or "http 401".
	Kind string
	Err  error
}

func (f *Failure) Error() string { return f.Err.Error() }

func (f *Failure) Unwrap() error { return f.Err }

// Away tells whether the user is away from the desk.
type Away interface {
	IsAway(ctx context.Context) (bool, error)
}

// Router is the Notifier that picks the channel for each message: the desktop,
// or Telegram while the user is away. The state is asked for every message, so
// `baton away` and `baton back` take effect without restarting the watcher.
//
// When Telegram fails, the message goes to the desktop instead (somebody may
// be back at the desk, and the notification waits in the desktop's list), and
// the failure is returned as a warning the first time its kind is seen, until
// a message gets through again.
type Router struct {
	desktop  Notifier
	telegram Notifier
	away     Away

	// warned holds the kinds of failure already reported since the last
	// success.
	warned map[string]bool
}

// NewRouter creates the router over the two channels and the away state.
func NewRouter(desktop, telegram Notifier, away Away) *Router {
	return &Router{desktop: desktop, telegram: telegram, away: away, warned: map[string]bool{}}
}

// Notify sends the message through the channel the away state selects.
func (r *Router) Notify(ctx context.Context, m Message) error {
	away, err := r.away.IsAway(ctx)
	if err != nil {
		return errors.Join(r.warnOnce("away state", fmt.Errorf("away state: %w; notifying on the desktop", err)), r.desktop.Notify(ctx, m))
	}
	if !away {
		clear(r.warned)
		return r.desktop.Notify(ctx, m)
	}

	err = r.telegram.Notify(ctx, m)
	if err == nil {
		clear(r.warned)
		return nil
	}
	kind := "telegram"
	var f *Failure
	if errors.As(err, &f) {
		kind = f.Kind
	}
	warning := r.warnOnce(kind, fmt.Errorf("telegram: %w; notifying on the desktop instead", err))
	return errors.Join(warning, r.desktop.Notify(ctx, m))
}

// warnOnce returns err the first time a kind of failure is seen, and nil
// after that.
func (r *Router) warnOnce(kind string, err error) error {
	if r.warned[kind] {
		return nil
	}
	r.warned[kind] = true
	return err
}
