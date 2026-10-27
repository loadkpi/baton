// Package configbus loads the Baton configuration.
package configbus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/runtime"
	"github.com/loadkpi/baton/business/types/sound"
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
	// Projects are the per-Project settings, in the order of the names.
	Projects []ProjectSettings
	// Telegram is where `baton watch` sends notifications while the user is
	// away. Nil means the config has no [telegram] table.
	Telegram *Telegram
	// StartTimeout is how long `baton new` and `baton handoff` wait for the
	// program of a new Guake tab to show up; DefaultStartTimeout when the
	// config has no [terminal] table.
	StartTimeout time.Duration
}

// DefaultStartTimeout is the start timeout of a config that sets none.
const DefaultStartTimeout = 10 * time.Second

// Telegram is the [telegram] table of the config. It holds no secret: the bot
// token is named by TokenVar and lives in the secrets file.
type Telegram struct {
	// APIURL is the base URL of the Bot API, without a trailing slash.
	APIURL string
	// TokenVar is the name of the variable in the secrets file that holds the
	// bot token.
	TokenVar string
	// ChatID is the chat that receives the messages: a number, or an @name.
	ChatID string
}

// TelegramSettings are the Telegram settings with the bot token resolved from
// the secrets file. The token is a secret: never print it.
type TelegramSettings struct {
	APIURL string
	ChatID string
	Token  string
}

// Watch is the [watch] table of the config: the settings of `baton watch`. It
// is not part of Config because it is read, and validated, only by the command
// that uses it (see Bus.Watch).
type Watch struct {
	// Sound is played after each desktop notification. The zero value means
	// none.
	Sound sound.Sound
}

// ProjectSettings are the settings of one Project in the config.
type ProjectSettings struct {
	Name projectname.Name
	// Tags are the tags the Profiles inside the Project's sandbox accept. None
	// means they receive only untagged items.
	Tags []tag.Tag
	// Compose is the Project's Docker Compose service that runs Agents; nil
	// when the Project has none.
	Compose *ComposeSettings
}

// ComposeSettings say where a Project's Agents run in its own Docker Compose
// setup and which Profiles live in that container (DESIGN.md, ticket 16).
type ComposeSettings struct {
	// Files are the Compose files, as written in the config: absolute, "~/..."
	// or relative to the Project's root.
	Files []string
	// Service is the name of the service in them.
	Service string
	// Profiles are the Profiles inside the container, in the order of their
	// names: the Project's own and those of a volume it hosts for others. A
	// Profile's Dir is a path in the container: absolute, or "~" / "~/..." for
	// the container user's HOME, which only the container knows. A Codex
	// Profile's Dir may be empty: the service's CODEX_HOME, else ~/.codex.
	Profiles []Profile
}

// ComposeOf returns the Compose settings of a Project, or nil.
func (c Config) ComposeOf(project projectname.Name) *ComposeSettings {
	for _, p := range c.Projects {
		if p.Name == project {
			return p.Compose
		}
	}
	return nil
}

// DefaultRuntime is the Runtime a Project's Agents run in unless told
// otherwise: compose for a Project that has a Compose service in the config,
// else the one found when the Project was registered (sandbox if it has an
// ai-sandbox, host if not).
func (c Config) DefaultRuntime(project projectname.Name, found runtime.Runtime) runtime.Runtime {
	if c.ComposeOf(project) != nil {
		return runtime.Compose
	}
	return found
}

// SandboxTags returns the tags the sandbox Profiles of a Project accept.
func (c Config) SandboxTags(project projectname.Name) []tag.Tag {
	for _, p := range c.Projects {
		if p.Name == project {
			return p.Tags
		}
	}
	return nil
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
	// QuerySecret returns the value of a variable of the secrets file, and
	// whether it is set there.
	QuerySecret(ctx context.Context, name string) (value string, ok bool, err error)
	// QueryWatch returns the [watch] table. Only this call validates it, so
	// that a mistake in it does not stop the commands that never read it.
	QueryWatch(ctx context.Context) (Watch, error)
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

// TelegramSettings returns the Telegram settings with the token. It fails,
// without printing any secret, when the config has no [telegram] table or the
// secrets file does not set the variable the table names.
func (b *Bus) TelegramSettings(ctx context.Context) (TelegramSettings, error) {
	cfg, err := b.Load(ctx)
	if err != nil {
		return TelegramSettings{}, err
	}
	if cfg.Telegram == nil {
		return TelegramSettings{}, errors.New("telegram is not configured; add a [telegram] table to the config (see DESIGN.md, \"Telegram while away\")")
	}
	token, ok, err := b.storer.QuerySecret(ctx, cfg.Telegram.TokenVar)
	if err != nil {
		return TelegramSettings{}, fmt.Errorf("telegram: %w", err)
	}
	if !ok || token == "" {
		return TelegramSettings{}, fmt.Errorf("telegram: %s is not set in the secrets file", cfg.Telegram.TokenVar)
	}
	return TelegramSettings{APIURL: cfg.Telegram.APIURL, ChatID: cfg.Telegram.ChatID, Token: token}, nil
}

// Watch returns the settings of `baton watch`. It fails when the [watch] table
// is wrong; a config without the table, or without the file, gives the empty
// settings.
func (b *Bus) Watch(ctx context.Context) (Watch, error) {
	w, err := b.storer.QueryWatch(ctx)
	if err != nil {
		return Watch{}, fmt.Errorf("load config: %w", err)
	}
	return w, nil
}
