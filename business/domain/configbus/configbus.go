// Package configbus loads the Baton configuration.
package configbus

import (
	"context"
	"fmt"

	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
)

// Config is the validated Baton configuration.
type Config struct {
	// Source is the absolute path of the user's source repository.
	Source string
	// Profiles are the host Profiles, in the order of their names.
	Profiles []Profile
}

// Profile is a host Profile: an Agent's config directory.
type Profile struct {
	Name  profilename.Name
	Agent agent.Agent
	// Dir is the absolute path of the Profile directory.
	Dir string
}

// Storer reads the configuration from its storage.
type Storer interface {
	Query(ctx context.Context) (Config, error)
}

// Bus is the configuration domain.
type Bus struct {
	storer Storer
}

// New creates the configuration domain over a storage.
func New(storer Storer) *Bus {
	return &Bus{storer: storer}
}

// Load returns the configuration or an error that tells the user what to fix.
func (b *Bus) Load(ctx context.Context) (Config, error) {
	cfg, err := b.storer.Query(ctx)
	if err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}
