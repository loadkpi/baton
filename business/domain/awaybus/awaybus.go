// Package awaybus keeps the one bit of state `baton away` and `baton back`
// share with `baton watch`: whether the user is away from the desk, which
// sends notifications to Telegram instead of the desktop (DESIGN.md, D6).
package awaybus

import (
	"context"
	"time"
)

// Storer keeps the state.
type Storer interface {
	// Set records that the user is away since the given time.
	Set(ctx context.Context, since time.Time) error
	// Clear records that the user is back. Clearing when not away is not an
	// error.
	Clear(ctx context.Context) error
	// Query tells whether the user is away.
	Query(ctx context.Context) (bool, error)
}

// Bus is the away domain.
type Bus struct {
	storer Storer
}

// New creates the domain over a storage.
func New(storer Storer) *Bus {
	return &Bus{storer: storer}
}

// Away records that the user is away.
func (b *Bus) Away(ctx context.Context) error {
	return b.storer.Set(ctx, time.Now().UTC())
}

// Back records that the user is back.
func (b *Bus) Back(ctx context.Context) error {
	return b.storer.Clear(ctx)
}

// IsAway tells whether the user is away. It reads the state every time, so a
// long-running `baton watch` sees a change made by another process.
func (b *Bus) IsAway(ctx context.Context) (bool, error) {
	return b.storer.Query(ctx)
}
