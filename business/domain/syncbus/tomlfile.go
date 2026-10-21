package syncbus

import (
	"context"
	"fmt"
	"strings"

	"github.com/loadkpi/baton/foundation/textdiff"
	"github.com/loadkpi/baton/foundation/tomlkeys"
)

// codexConfigFile is Codex's configuration, relative to the Profile
// directory: where its MCP servers are, as [mcp_servers.<name>] tables.
const codexConfigFile = "config.toml"

// planMCPTables plans the MCP servers of a Codex Profile whose config.toml is
// edited in place: one table per server, in the file shared with Codex and the
// user. The servers are converted by the same code as the sandbox's fragment
// (codexServers); the rules of the file are those of planKeys, and the editor
// is foundation/tomlkeys.
func (b *Bus) planMCPTables(ctx context.Context, plan *profilePlan, previous Manifest, wanted mcpServers, secrets Secrets, res *Result) error {
	values, warnings := codexServers(wanted.names, wanted.values, true)
	for _, w := range warnings {
		res.Warnings = append(res.Warnings, fmt.Sprintf("profile %s: %s", plan.profile.Name, w))
	}
	templates, _ := codexServers(wanted.names, wanted.templates, false)

	keys := make([]wantedKey, 0, len(values))
	for _, name := range wanted.names {
		if table, ok := values[name]; ok {
			keys = append(keys, wantedKey{Key: []string{"mcp_servers", name}, Value: table, Template: templates[name]})
		}
	}
	return b.planKeys(ctx, plan, previous, keysTarget{
		rel:        codexConfigFile,
		backupRel:  codexConfigFile,
		name:       codexConfigFile,
		wanted:     keys,
		stale:      staleServers(previous, codexConfigFile, "mcp_servers", func(name string) bool { _, ok := values[name]; return ok }),
		mode:       0o600, // it holds secrets
		hasSecrets: wanted.hasSecrets,
		hidden:     secrets.Values,
		toml:       true,
	})
}

// editTables applies the target to a TOML document: the same decisions as
// editKeys, with tables for keys.
func editTables(data []byte, previous Manifest, t keysTarget) (keysEdit, error) {
	edit := keysEdit{data: data}
	var oldView, newView strings.Builder
	var labels []string
	show := func(label, before, after string) {
		labels = append(labels, label)
		oldView.WriteString(hide(before, t.hidden))
		newView.WriteString(after)
	}

	for _, w := range t.wanted {
		table, found, err := tomlkeys.Get(edit.data, w.Key)
		if err != nil {
			return keysEdit{}, err
		}
		if found && sumOfJSON(table) == sumOfJSON(w.Value) {
			edit.unchanged++
			continue
		}
		if mustAsk(previous, t.rel, w, found, sumOfJSON(table)) {
			edit.needsAsk = true
		}
		template := w.Template
		if template == nil {
			template = w.Value
		}
		after, err := tomlkeys.Format(w.Key, asTable(template))
		if err != nil {
			return keysEdit{}, err
		}
		before, err := tomlView(w.Key, table, found, template)
		if err != nil {
			return keysEdit{}, err
		}
		show(strings.Join(w.Key, "."), before, after+"\n")
		if edit.data, err = tomlkeys.Set(edit.data, w.Key, asTable(w.Value)); err != nil {
			return keysEdit{}, err
		}
	}

	for _, k := range t.stale {
		table, found, err := tomlkeys.Get(edit.data, k.Key)
		if err != nil {
			return keysEdit{}, err
		}
		if !found {
			continue
		}
		if sumOfJSON(table) != k.Sum {
			edit.needsAsk = true
		}
		before, err := tomlView(k.Key, table, found, nil)
		if err != nil {
			return keysEdit{}, err
		}
		show(strings.Join(k.Key, "."), before, "# "+strings.Join(k.Key, ".")+" (removed)\n\n")
		if edit.data, err = tomlkeys.Delete(edit.data, k.Key); err != nil {
			return keysEdit{}, err
		}
	}

	edit.changed = len(labels) > 0
	if edit.changed {
		edit.diff = textdiff.Unified("a/"+t.name, "b/"+t.name, oldView.String(), newView.String())
		if edit.diff == "" {
			// What differs is hidden: only values where a secret goes.
			edit.diff = "only secret values differ in " + strings.Join(labels, ", ") + "\n"
		}
	}
	return edit, nil
}

// tomlView is the table as a question shows it before the change: as TOML,
// with every string that sits where the template has a ${VAR} placeholder
// replaced by the template's text. Absent when it is not in the file.
func tomlView(key []string, table map[string]any, found bool, template any) (string, error) {
	if !found {
		return "# " + strings.Join(key, ".") + " (absent)\n\n", nil
	}
	covered, _ := coverSecrets(table, decodeJSON(compactJSON(template))).(map[string]any)
	text, err := tomlkeys.Format(key, covered)
	if err != nil {
		return "", err
	}
	return text + "\n", nil
}

// asTable is the value of a wanted key, which is a table for a TOML file.
func asTable(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
