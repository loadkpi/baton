// Package configfile reads the Baton configuration from its TOML file.
package configfile

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/tag"
	"github.com/loadkpi/baton/foundation/secretsenv"
)

// Store reads the configuration file at a path.
type Store struct {
	path string
	home string
}

// secretsRel is where the secrets file lives, relative to home.
const secretsRel = ".config/baton/secrets.env"

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
	Projects       map[string]fileProject `toml:"projects"`
	Telegram       *fileTelegram          `toml:"telegram"`
}

// fileTelegram is the [telegram] table. chat_id is a number or a string, so
// it is decoded as it comes.
type fileTelegram struct {
	APIURL   string `toml:"api_url"`
	TokenVar string `toml:"token_var"`
	ChatID   any    `toml:"chat_id"`
}

type fileProject struct {
	Tags []string `toml:"tags"`
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

// QuerySecret reads one variable of the secrets file.
func (s *Store) QuerySecret(_ context.Context, name string) (string, bool, error) {
	values, err := secretsenv.Read(filepath.Join(s.home, secretsRel))
	if err != nil {
		return "", false, err
	}
	v, ok := values[name]
	return v, ok, nil
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
	projects := make([]string, 0, len(fc.Projects))
	for name := range fc.Projects {
		projects = append(projects, name)
	}
	sort.Strings(projects)
	for _, name := range projects {
		p, err := toBusProjectSettings(name, fc.Projects[name])
		if err != nil {
			return configbus.Config{}, err
		}
		cfg.Projects = append(cfg.Projects, p)
	}
	if fc.Telegram != nil {
		t, err := toBusTelegram(*fc.Telegram)
		if err != nil {
			return configbus.Config{}, fmt.Errorf("telegram: %w", err)
		}
		cfg.Telegram = &t
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

// defaultAPIURL is the Bot API of Telegram.
const defaultAPIURL = "https://api.telegram.org"

func toBusTelegram(ft fileTelegram) (configbus.Telegram, error) {
	t := configbus.Telegram{APIURL: defaultAPIURL}
	if ft.APIURL != "" {
		u, err := parseAPIURL(ft.APIURL)
		if err != nil {
			return configbus.Telegram{}, err
		}
		t.APIURL = u
	}
	// A bot token pasted here would be a secret in a file that may be
	// committed, so the value is never echoed back.
	if !secretsenv.ValidName(ft.TokenVar) {
		return configbus.Telegram{}, errors.New("token_var is required and must be the NAME of a variable in secrets.env (the token itself goes there, not here)")
	}
	t.TokenVar = ft.TokenVar
	switch id := ft.ChatID.(type) {
	case int64:
		t.ChatID = strconv.FormatInt(id, 10)
	case string:
		t.ChatID = id
	}
	if t.ChatID == "" {
		return configbus.Telegram{}, errors.New("chat_id is required (a number, or an @name in a string)")
	}
	return t, nil
}

// parseAPIURL accepts an https URL, or an http one for this machine only, so
// that the bot token, which is part of every request path, cannot be sent in
// the clear over a network.
func parseAPIURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("api_url must be a URL like https://api.telegram.org")
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && isLoopback(u.Hostname()):
	default:
		return "", errors.New("api_url must use https (http is accepted only for localhost)")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func toBusProjectSettings(name string, fp fileProject) (configbus.ProjectSettings, error) {
	pn, err := projectname.Parse(name)
	if err != nil {
		return configbus.ProjectSettings{}, fmt.Errorf("projects.%s: %w", name, err)
	}
	tags, err := tag.ParseAll(fp.Tags)
	if err != nil {
		return configbus.ProjectSettings{}, fmt.Errorf("projects.%s: tags: %w", name, err)
	}
	return configbus.ProjectSettings{Name: pn, Tags: tags}, nil
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
	if a == agent.Codex {
		// Codex runs only in a sandbox, whose Profiles come from the
		// registered Projects, not from the config (DESIGN.md, D3).
		return configbus.Profile{}, fmt.Errorf("profile %s: agent %q runs only in a sandbox, whose Profiles are not listed in the config", name, a)
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
