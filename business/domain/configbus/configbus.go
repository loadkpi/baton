// Package configbus loads the Baton configuration.
package configbus

import (
	"context"
	"fmt"
	"strings"

	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/tag"
)

// Config is the validated Baton configuration.
type Config struct {
	// Home is the home directory of the user.
	Home string
	// Source is the absolute path of the user's source repository.
	Source string
	// DefaultProfile is the Profile `baton new` uses when none is chosen. The
	// zero value means none is configured.
	DefaultProfile profilename.Name
	// Profiles are the host Profiles, in the order of their names.
	Profiles []Profile
}

// Profile is a host Profile: an Agent's config directory.
type Profile struct {
	Name  profilename.Name
	Agent agent.Agent
	// Dir is the absolute path of the Profile directory.
	Dir string
	// Tags are the tags the Profile accepts. A Profile with none receives
	// only untagged items.
	Tags []tag.Tag
}

// SelectProfile returns the Profile of an Agent to start a Session under:
// the chosen one, else the configured default, else the only Profile of the
// Agent. chosen is the zero value when the user chose none.
func (c Config) SelectProfile(a agent.Agent, chosen profilename.Name) (Profile, error) {
	var candidates []Profile
	for _, p := range c.Profiles {
		if p.Agent == a {
			candidates = append(candidates, p)
		}
	}

	name := chosen
	if name == (profilename.Name{}) {
		name = c.DefaultProfile
	}
	if name != (profilename.Name{}) {
		for _, p := range candidates {
			if p.Name == name {
				return p, nil
			}
		}
		return Profile{}, fmt.Errorf("unknown profile %q (%s profiles: %s)", name, a, joinNames(candidates))
	}

	switch len(candidates) {
	case 0:
		return Profile{}, fmt.Errorf("no %s profile is configured", a)
	case 1:
		return candidates[0], nil
	default:
		return Profile{}, fmt.Errorf("several %s profiles (%s); choose one with --profile or set default_profile in the config", a, joinNames(candidates))
	}
}

func joinNames(profiles []Profile) string {
	names := make([]string, len(profiles))
	for i, p := range profiles {
		names[i] = p.Name.String()
	}
	return strings.Join(names, ", ")
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
