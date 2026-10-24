// Package configfile reads the Baton configuration from its TOML file.
package configfile

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/sound"
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
	Watch          *fileWatch             `toml:"watch"`
}

// fileWatch is the [watch] table. sound is decoded as it comes, so that a
// value of the wrong type is an error of `baton watch` only, not of every
// command that reads the file.
type fileWatch struct {
	Sound any `toml:"sound"`
}

// fileTelegram is the [telegram] table. chat_id is a number or a string, so
// it is decoded as it comes.
type fileTelegram struct {
	APIURL   string `toml:"api_url"`
	TokenVar string `toml:"token_var"`
	ChatID   any    `toml:"chat_id"`
}

type fileProject struct {
	Tags    []string     `toml:"tags"`
	Compose *fileCompose `toml:"compose"`
}

// fileCompose is [projects.<name>.compose]: the service and its Profiles.
type fileCompose struct {
	Files    []string                      `toml:"files"`
	Service  string                        `toml:"service"`
	Profiles map[string]fileComposeProfile `toml:"profiles"`
}

// fileComposeProfile is a Profile inside the container of the service.
type fileComposeProfile struct {
	Agent string   `toml:"agent"`
	Dir   string   `toml:"dir"`
	Tags  []string `toml:"tags"`
}

// fileProfile is a [profiles.<name>] table: a Profile of the host, or (with
// runtime = "compose" and project) a Profile in a volume that the Compose
// service of that Project hosts, configured here once for everybody who
// shares the volume.
type fileProfile struct {
	Agent   string   `toml:"agent"`
	Dir     string   `toml:"dir"`
	Tags    []string `toml:"tags"`
	Runtime string   `toml:"runtime"`
	Project string   `toml:"project"`
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

// QueryWatch reads the [watch] table and validates it. Query leaves its
// values alone, so that a wrong one stops `baton watch` and nothing else.
func (s *Store) QueryWatch(_ context.Context) (configbus.Watch, error) {
	var fc fileConfig
	md, err := toml.DecodeFile(s.path, &fc)
	if err != nil {
		if os.IsNotExist(err) {
			return configbus.Watch{}, nil
		}
		return configbus.Watch{}, fmt.Errorf("%s: %w", s.path, err)
	}
	for _, key := range md.Undecoded() {
		if key[0] == "watch" {
			return configbus.Watch{}, fmt.Errorf("%s: unknown key %q", s.path, key.String())
		}
	}
	if fc.Watch == nil {
		return configbus.Watch{}, nil
	}
	w, err := toBusWatch(*fc.Watch)
	if err != nil {
		return configbus.Watch{}, fmt.Errorf("%s: %w", s.path, err)
	}
	return w, nil
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
	cfg := configbus.Config{Home: home, Source: source}

	names := make([]string, 0, len(fc.Profiles))
	for name := range fc.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	shared := make(map[string][]configbus.Profile) // volume Profiles by the Project that hosts them
	for _, name := range names {
		fp := fc.Profiles[name]
		switch fp.Runtime {
		case "", "host":
			if fp.Project != "" {
				return configbus.Config{}, fmt.Errorf("profile %s: project applies only to runtime = \"compose\"", name)
			}
			p, err := toBusProfile(name, fp, home)
			if err != nil {
				return configbus.Config{}, err
			}
			cfg.Profiles = append(cfg.Profiles, p)
		case "compose":
			p, err := toBusComposeProfile(name, fp.Agent, fp.Dir, fp.Tags)
			if err != nil {
				return configbus.Config{}, err
			}
			if fp.Project == "" {
				return configbus.Config{}, fmt.Errorf("profile %s: project is required (the Project whose Compose service hosts the volume)", name)
			}
			shared[fp.Project] = append(shared[fp.Project], p)
		default:
			return configbus.Config{}, fmt.Errorf("profile %s: runtime %q must be \"host\" or \"compose\" (sandbox Profiles are not listed in the config)", name, fp.Runtime)
		}
	}
	projects := make([]string, 0, len(fc.Projects))
	for name := range fc.Projects {
		projects = append(projects, name)
	}
	sort.Strings(projects)
	for _, name := range projects {
		p, err := toBusProjectSettings(name, fc.Projects[name], shared[name])
		if err != nil {
			return configbus.Config{}, err
		}
		delete(shared, name)
		cfg.Projects = append(cfg.Projects, p)
	}
	for project, ps := range shared {
		return configbus.Config{}, fmt.Errorf("profile %s: project %q has no [projects.%s.compose] table", ps[0].Name, project, project)
	}
	if err := checkProfiles(cfg); err != nil {
		return configbus.Config{}, err
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
		if _, ok := fc.Profiles[fc.DefaultProfile]; !ok || !isHost(fc.Profiles[fc.DefaultProfile]) {
			return configbus.Config{}, fmt.Errorf("default_profile %q is not a configured profile", fc.DefaultProfile)
		}
		cfg.DefaultProfile = name
	}
	return cfg, nil
}

// toBusWatch converts the [watch] table. The value of a wrong sound is not
// repeated in the error.
func toBusWatch(fw fileWatch) (configbus.Watch, error) {
	var raw string
	switch v := fw.Sound.(type) {
	case nil:
	case string:
		raw = v
	default:
		return configbus.Watch{}, errors.New("watch.sound must be a string: a name from the sound theme or a path")
	}
	snd, err := sound.Parse(raw)
	if err != nil {
		return configbus.Watch{}, fmt.Errorf("watch.sound: %w", err)
	}
	return configbus.Watch{Sound: snd}, nil
}

func isHost(fp fileProfile) bool { return fp.Runtime == "" || fp.Runtime == "host" }

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

func toBusProjectSettings(name string, fp fileProject, hosted []configbus.Profile) (configbus.ProjectSettings, error) {
	pn, err := projectname.Parse(name)
	if err != nil {
		return configbus.ProjectSettings{}, fmt.Errorf("projects.%s: %w", name, err)
	}
	tags, err := tag.ParseAll(fp.Tags)
	if err != nil {
		return configbus.ProjectSettings{}, fmt.Errorf("projects.%s: tags: %w", name, err)
	}
	ps := configbus.ProjectSettings{Name: pn, Tags: tags}
	if fp.Compose == nil {
		return ps, nil
	}
	if ps.Compose, err = toBusCompose(name, *fp.Compose, hosted); err != nil {
		return configbus.ProjectSettings{}, fmt.Errorf("projects.%s.compose: %w", name, err)
	}
	return ps, nil
}

// toBusCompose converts a Project's Compose table. hosted are the Profiles of
// volumes the service hosts for several Compose setups, from [profiles.*].
func toBusCompose(project string, fc fileCompose, hosted []configbus.Profile) (*configbus.ComposeSettings, error) {
	if len(fc.Files) == 0 {
		return nil, errors.New("files is required (the Compose files, as for docker compose -f)")
	}
	for _, f := range fc.Files {
		if f == "" {
			return nil, errors.New("files: a file name is empty")
		}
	}
	if fc.Service == "" {
		return nil, errors.New("service is required")
	}
	c := &configbus.ComposeSettings{Files: fc.Files, Service: fc.Service, Profiles: hosted}
	for key, fp := range fc.Profiles {
		name := project + "." + key
		p, err := toBusComposeProfile(name, fp.Agent, fp.Dir, fp.Tags)
		if err != nil {
			return nil, err
		}
		c.Profiles = append(c.Profiles, p)
	}
	if len(c.Profiles) == 0 {
		return nil, errors.New("no Profiles: add [projects." + project + ".compose.profiles.<name>], or a [profiles.<name>] with runtime = \"compose\" and project = \"" + project + "\"")
	}
	sort.Slice(c.Profiles, func(i, j int) bool { return c.Profiles[i].Name.String() < c.Profiles[j].Name.String() })
	return c, nil
}

// toBusComposeProfile converts a Profile inside a container. Its Dir stays a
// path of the container: "~" is the HOME of the container's user, which the
// host cannot know. A Codex Profile may leave it empty, which means the
// service's CODEX_HOME, else ~/.codex.
func toBusComposeProfile(name, agentName, dir string, tags []string) (configbus.Profile, error) {
	pn, err := profilename.Parse(name)
	if err != nil {
		return configbus.Profile{}, err
	}
	a, err := agent.Parse(agentName)
	if err != nil {
		return configbus.Profile{}, fmt.Errorf("profile %s: %w", name, err)
	}
	switch {
	case dir == "" && a == agent.Codex:
		// Codex's home is CODEX_HOME of the service, else ~/.codex; only the
		// container knows, so it is found when the Profile is used.
	case dir == "~":
	case strings.HasPrefix(dir, "~/"):
		dir = "~/" + strings.TrimPrefix(path.Clean("/"+dir[2:]), "/")
		dir = strings.TrimSuffix(dir, "/")
		if dir == "~" {
			break
		}
	case path.IsAbs(dir):
		dir = path.Clean(dir)
	default:
		return configbus.Profile{}, fmt.Errorf("profile %s: dir %q must be an absolute path in the container (or start with ~, the HOME of its user)", name, dir)
	}
	ts, err := tag.ParseAll(tags)
	if err != nil {
		return configbus.Profile{}, fmt.Errorf("profile %s: tags: %w", name, err)
	}
	return configbus.Profile{Name: pn, Agent: a, Dir: dir, Tags: ts}, nil
}

// checkProfiles requires at least one Profile and refuses a name used twice,
// because a Profile name names its manifest.
func checkProfiles(cfg configbus.Config) error {
	seen := make(map[string]bool)
	add := func(p configbus.Profile) error {
		if seen[p.Name.String()] {
			return fmt.Errorf("profile %s is configured twice", p.Name)
		}
		seen[p.Name.String()] = true
		return nil
	}
	for _, p := range cfg.Profiles {
		if err := add(p); err != nil {
			return err
		}
	}
	for _, ps := range cfg.Projects {
		if ps.Compose == nil {
			continue
		}
		for _, p := range ps.Compose.Profiles {
			if err := add(p); err != nil {
				return err
			}
		}
	}
	if len(seen) == 0 {
		return errors.New("no profiles configured; add a [profiles.<name>] table")
	}
	return nil
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
