// Package configfile reads the Baton configuration from its TOML file.
package configfile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/tag"
)

// Store reads the configuration file at a path.
type Store struct {
	path string
	home string
}

// New creates a store for the configuration file at path. Paths inside the
// file may start with "~", which stands for home.
func New(path, home string) *Store {
	return &Store{path: path, home: home}
}

// fileConfig is the TOML document. Primitive types only.
type fileConfig struct {
	Source         string                 `toml:"source"`
	DefaultProfile string                 `toml:"default_profile"`
	Profiles       map[string]fileProfile `toml:"profiles"`
}

type fileProfile struct {
	Agent string   `toml:"agent"`
	Dir   string   `toml:"dir"`
	Tags  []string `toml:"tags"`
}

// Query reads and validates the configuration file.
func (s *Store) Query(_ context.Context) (configbus.Config, error) {
	var fc fileConfig
	md, err := toml.DecodeFile(s.path, &fc)
	if err != nil {
		if os.IsNotExist(err) {
			return configbus.Config{}, fmt.Errorf("%s does not exist; create it (see DESIGN.md, \"Baton config\")", s.path)
		}
		return configbus.Config{}, fmt.Errorf("%s: %w", s.path, err)
	}

	// A misspelled key must not be silently ignored.
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return configbus.Config{}, fmt.Errorf("%s: unknown key %q", s.path, undecoded[0].String())
	}

	cfg, err := toBusConfig(fc, s.home)
	if err != nil {
		return configbus.Config{}, fmt.Errorf("%s: %w", s.path, err)
	}
	return cfg, nil
}

// toBusConfig converts the file document into the validated business type.
func toBusConfig(fc fileConfig, home string) (configbus.Config, error) {
	if fc.Source == "" {
		return configbus.Config{}, errors.New("source is required (the path of your source repository)")
	}
	source := expandHome(fc.Source, home)
	if !filepath.IsAbs(source) {
		return configbus.Config{}, fmt.Errorf("source %q must be an absolute path (or start with ~)", fc.Source)
	}
	if len(fc.Profiles) == 0 {
		return configbus.Config{}, errors.New("no profiles configured; add a [profiles.<name>] table")
	}
	cfg := configbus.Config{Home: home, Source: source}

	names := make([]string, 0, len(fc.Profiles))
	for name := range fc.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		p, err := toBusProfile(name, fc.Profiles[name], home)
		if err != nil {
			return configbus.Config{}, err
		}
		cfg.Profiles = append(cfg.Profiles, p)
	}
	if fc.DefaultProfile != "" {
		name, err := profilename.Parse(fc.DefaultProfile)
		if err != nil {
			return configbus.Config{}, fmt.Errorf("default_profile: %w", err)
		}
		if _, ok := fc.Profiles[fc.DefaultProfile]; !ok {
			return configbus.Config{}, fmt.Errorf("default_profile %q is not a configured profile", fc.DefaultProfile)
		}
		cfg.DefaultProfile = name
	}
	return cfg, nil
}

func toBusProfile(name string, fp fileProfile, home string) (configbus.Profile, error) {
	pn, err := profilename.Parse(name)
	if err != nil {
		return configbus.Profile{}, err
	}
	a, err := agent.Parse(fp.Agent)
	if err != nil {
		return configbus.Profile{}, fmt.Errorf("profile %s: %w", name, err)
	}
	if fp.Dir == "" {
		return configbus.Profile{}, fmt.Errorf("profile %s: dir is required", name)
	}
	dir := expandHome(fp.Dir, home)
	if !filepath.IsAbs(dir) {
		return configbus.Profile{}, fmt.Errorf("profile %s: dir %q must be an absolute path (or start with ~)", name, fp.Dir)
	}
	tags, err := tag.ParseAll(fp.Tags)
	if err != nil {
		return configbus.Profile{}, fmt.Errorf("profile %s: tags: %w", name, err)
	}
	return configbus.Profile{Name: pn, Agent: a, Dir: dir, Tags: tags}, nil
}

// expandHome replaces a leading "~" or "~/" with the home directory.
func expandHome(p, home string) string {
	switch {
	case p == "~":
		return home
	case len(p) > 1 && p[:2] == "~/":
		return filepath.Join(home, p[2:])
	default:
		return p
	}
}
