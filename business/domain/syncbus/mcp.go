package syncbus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/loadkpi/baton/business/domain/configbus"
	"github.com/loadkpi/baton/business/types/tag"
	"github.com/loadkpi/baton/foundation/jsonkeys"
	"github.com/loadkpi/baton/foundation/textdiff"
)

// mcpFile is the name of the file where a Claude Profile keeps its user-scope
// MCP servers.
const mcpFile = ".claude.json"

// mcpHomeFile is how the manifest names ~/.claude.json, the file of the default
// Profile (~/.claude), relative to the Profile directory.
const mcpHomeFile = "../" + mcpFile

// mcpRel is where the Profile's MCP configuration is, relative to the Profile
// directory. A Profile used without CLAUDE_CONFIG_DIR, which is ~/.claude,
// keeps it in the home directory; every other Profile keeps it inside itself.
// Observed with Claude Code 2.1.304, not documented by the vendor.
func mcpRel(profileDir, home string) string {
	if profileDir == filepath.Join(home, ".claude") {
		return mcpHomeFile
	}
	return mcpFile
}

// MCPServer is one server of the source's MCP manifest.
type MCPServer struct {
	Name string
	// Tags decide which Profiles receive the server; none means all of them.
	Tags []tag.Tag
	// Spec is the server's configuration as the source has it, with ${VAR}
	// placeholders instead of secrets.
	Spec map[string]any
}

// Secrets are the values of the ${VAR} placeholders.
type Secrets struct {
	// Path is the secrets file, for messages.
	Path   string
	Values map[string]string
}

var placeholder = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// substitute returns spec with every placeholder replaced by its value.
func substitute(server MCPServer, secrets Secrets) (map[string]any, error) {
	var missing error
	var walk func(v any) any
	walk = func(v any) any {
		switch v := v.(type) {
		case string:
			return placeholder.ReplaceAllStringFunc(v, func(m string) string {
				name := placeholder.FindStringSubmatch(m)[1]
				value, ok := secrets.Values[name]
				if !ok && missing == nil {
					missing = fmt.Errorf("mcp.json: server %q uses ${%s}, which is not set in %s", server.Name, name, secrets.Path)
				}
				return value
			})
		case map[string]any:
			out := make(map[string]any, len(v))
			for k, item := range v {
				out[k] = walk(item)
			}
			return out
		case []any:
			out := make([]any, len(v))
			for i, item := range v {
				out[i] = walk(item)
			}
			return out
		default:
			return v
		}
	}
	out := walk(server.Spec).(map[string]any)
	return out, missing
}

// mcpServers are the servers one Profile receives.
type mcpServers struct {
	// names are sorted.
	names []string
	// values are the servers with their secrets in; templates are the same
	// with the ${VAR} placeholders, which is what a question shows.
	values    map[string]any
	templates map[string]any
	// hasSecrets is set when a placeholder was substituted in any of them.
	hasSecrets bool
}

// serversFor selects the servers of the source that a Profile accepts and
// substitutes their secrets. A variable missing for a server the Profile does
// not accept is nobody's problem.
func serversFor(profile configbus.Profile, source Source, secrets Secrets) (mcpServers, error) {
	got := mcpServers{values: make(map[string]any), templates: make(map[string]any)}
	for _, s := range source.MCP {
		if !tag.AcceptedBy(s.Tags, profile.Tags) {
			continue
		}
		spec, err := substitute(s, secrets)
		if err != nil {
			return mcpServers{}, err
		}
		got.hasSecrets = got.hasSecrets || hasPlaceholder(s.Spec)
		got.names = append(got.names, s.Name)
		got.values[s.Name] = spec
		got.templates[s.Name] = s.Spec
	}
	sort.Strings(got.names)
	return got, nil
}

func hasPlaceholder(v any) bool {
	switch v := v.(type) {
	case string:
		return placeholder.MatchString(v)
	case map[string]any:
		for _, item := range v {
			if hasPlaceholder(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if hasPlaceholder(item) {
				return true
			}
		}
	}
	return false
}

// staleServers are the servers recorded in the manifest that the Profile no
// longer receives.
func staleServers(previous Manifest, rel string, wanted mcpServers) []OwnedKey {
	var stale []OwnedKey
	for _, k := range previous.Keys {
		if k.Path != rel || len(k.Key) != 2 || k.Key[0] != "mcpServers" {
			continue
		}
		if _, ok := wanted.values[k.Key[1]]; !ok {
			stale = append(stale, k)
		}
	}
	return stale
}

// planMCP plans the servers of the source in the Profile's MCP configuration.
//
// The file is shared: Claude and the user keep their own things in it, and
// Baton owns the named keys under mcpServers that it recorded in the manifest.
// A file that does not exist yet is nobody's, so it is created without a
// question. Baton's keys are updated, added or removed without one while they
// are still what Baton wrote; anything else is a question, which shows only
// Baton's keys.
func (b *Bus) planMCP(ctx context.Context, plan *profilePlan, home string, previous Manifest, source Source, secrets Secrets) error {
	wanted, err := serversFor(plan.profile, source, secrets)
	if err != nil {
		return err
	}
	rel := mcpRel(plan.profile.Dir, home)
	stale := staleServers(previous, rel, wanted)

	for _, name := range wanted.names {
		plan.manifest.Keys = append(plan.manifest.Keys, OwnedKey{Path: rel, Key: []string{"mcpServers", name}, Sum: sumOfJSON(wanted.values[name])})
	}
	plan.keysBefore = append(slices.Clone(plan.manifest.Keys), stale...)
	if len(wanted.names) == 0 && len(stale) == 0 {
		return nil
	}

	file, err := b.storer.QueryShared(ctx, plan.profile.Dir, rel)
	if err != nil {
		return err
	}
	if !file.Exists {
		if len(wanted.names) == 0 {
			return nil
		}
		data, err := json.MarshalIndent(map[string]any{"mcpServers": wanted.values}, "", "  ")
		if err != nil {
			return err
		}
		// 0600 because it holds secrets.
		plan.shared = append(plan.shared, sharedChange{rel: rel, path: plan.abs(rel), data: append(data, '\n'), mode: 0o600})
		return nil
	}

	// Secrets are never written into a file others can read, and a file Baton
	// does not own is never chmod-ed: the user has to.
	if wanted.hasSecrets && file.Mode.Perm()&0o077 != 0 {
		return fmt.Errorf("%s is accessible by others (mode %04o) and would receive secrets; run: chmod 600 %s", file.Path, file.Mode.Perm(), file.Path)
	}

	edit, err := editServers(file, rel, previous, wanted, stale, secrets)
	if err != nil {
		return fmt.Errorf("%s: %w", file.Path, err)
	}
	plan.unchanged += edit.unchanged
	if !edit.changed {
		return nil
	}
	plan.shared = append(plan.shared, sharedChange{rel: rel, path: file.Path, data: edit.data, mode: file.Mode.Perm()})
	if edit.needsAsk {
		plan.asks = append(plan.asks, ask{
			rel:  mcpFile, // names the backup, so it must stay inside the backup directory
			path: file.Path,
			question: Question{
				Profile: plan.profile.Name.String(),
				Path:    plan.abs(rel),
				Diff:    edit.diff,
			},
		})
	}
	return nil
}

// serversEdit is the change to an existing MCP configuration.
type serversEdit struct {
	data      []byte
	unchanged int
	// needsAsk is set when a key that is changed was not written by Baton or
	// was edited since.
	needsAsk bool
	// changed is set when the file differs from what it was.
	changed bool
	// diff shows Baton's keys before and after, with secrets hidden.
	diff string
}

func editServers(file SharedFile, rel string, previous Manifest, wanted mcpServers, stale []OwnedKey, secrets Secrets) (serversEdit, error) {
	// Every value of the secrets file is hidden in what is shown, not only those
	// the Profile receives: a server that is gone from the source, or one edited
	// by hand, may hold any of them.
	hidden := secrets.Values

	edit := serversEdit{data: file.Data}
	var oldView, newView strings.Builder
	var labels []string
	show := func(label string, raw []byte, found bool, template any, after string) {
		labels = append(labels, label)
		if found {
			fmt.Fprintf(&oldView, "%s = %s\n", label, hide(prettyJSON(compactJSON(coverSecrets(decodeJSON(raw), template))), hidden))
		} else {
			fmt.Fprintf(&oldView, "%s = (absent)\n", label)
		}
		fmt.Fprintf(&newView, "%s = %s\n", label, after)
	}

	for _, name := range wanted.names {
		key := []string{"mcpServers", name}
		raw, found, err := jsonkeys.Get(edit.data, key)
		if err != nil {
			return serversEdit{}, err
		}
		if found && sumOfRaw(raw) == sumOfJSON(wanted.values[name]) {
			edit.unchanged++
			continue
		}
		if recorded, owned := recordedKeySum(previous, rel, key); !owned || !found || sumOfRaw(raw) != recorded {
			edit.needsAsk = true
		}
		show("mcpServers."+name, raw, found, wanted.templates[name], prettyJSON(compactJSON(wanted.templates[name])))
		if edit.data, err = jsonkeys.Set(edit.data, key, compactJSON(wanted.values[name])); err != nil {
			return serversEdit{}, err
		}
	}

	for _, k := range stale {
		raw, found, err := jsonkeys.Get(edit.data, k.Key)
		if err != nil {
			return serversEdit{}, err
		}
		if !found {
			continue
		}
		if sumOfRaw(raw) != k.Sum {
			edit.needsAsk = true
		}
		show("mcpServers."+k.Key[1], raw, found, nil, "(removed)")
		if edit.data, err = jsonkeys.Delete(edit.data, k.Key); err != nil {
			return serversEdit{}, err
		}
	}

	edit.changed = len(labels) > 0
	if edit.changed {
		edit.diff = textdiff.Unified("a/"+mcpFile, "b/"+mcpFile, oldView.String(), newView.String())
		if edit.diff == "" {
			// What differs is hidden: only values where a secret goes.
			edit.diff = "only secret values differ in " + strings.Join(labels, ", ") + "\n"
		}
	}
	return edit, nil
}

// compactJSON is the canonical form of a JSON value: no white space, object
// keys sorted.
func compactJSON(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err) // v is built from decoded JSON
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func sumOfJSON(v any) string {
	h := sha256.Sum256(compactJSON(v))
	return hex.EncodeToString(h[:])
}

// sumOfRaw is sumOfJSON of a value as it stands in a document, so formatting
// does not matter.
func sumOfRaw(raw []byte) string {
	return sumOfJSON(decodeJSON(raw))
}

// prettyJSON indents compact or formatted JSON for showing.
func prettyJSON(raw []byte) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}

// hide replaces the values of secrets in text with their ${NAME}, so that a
// question never shows what is in the secrets file. Values are matched as they
// appear inside a JSON string, longest first.
func hide(text string, secrets map[string]string) string {
	type entry struct{ name, escaped string }
	var entries []entry
	for name, value := range secrets {
		if value == "" {
			continue
		}
		quoted, _ := json.Marshal(value)
		entries = append(entries, entry{name, string(quoted[1 : len(quoted)-1])})
	}
	sort.Slice(entries, func(i, j int) bool { return len(entries[i].escaped) > len(entries[j].escaped) })
	for _, e := range entries {
		text = strings.ReplaceAll(text, e.escaped, "${"+e.name+"}")
	}
	return text
}

func recordedKeySum(m Manifest, rel string, key []string) (string, bool) {
	for _, k := range m.Keys {
		if k.Path == rel && slices.Equal(k.Key, key) {
			return k.Sum, true
		}
	}
	return "", false
}

func decodeJSON(raw []byte) any {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil
	}
	return v
}

// coverSecrets returns value with every string that sits where the template
// has a ${VAR} placeholder replaced by the template's text. Whatever is there
// (the value Baton wrote, an older one, one pasted by hand) is a secret and is
// never shown.
func coverSecrets(value, template any) any {
	switch t := template.(type) {
	case string:
		if _, ok := value.(string); ok && placeholder.MatchString(t) {
			return t
		}
	case map[string]any:
		if v, ok := value.(map[string]any); ok {
			out := make(map[string]any, len(v))
			for k, item := range v {
				out[k] = coverSecrets(item, t[k])
			}
			return out
		}
	case []any:
		if v, ok := value.([]any); ok {
			out := make([]any, len(v))
			for i, item := range v {
				var sub any
				if i < len(t) {
					sub = t[i]
				}
				out[i] = coverSecrets(item, sub)
			}
			return out
		}
	}
	return value
}
