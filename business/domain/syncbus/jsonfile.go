package syncbus

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/loadkpi/baton/foundation/jsonkeys"
	"github.com/loadkpi/baton/foundation/textdiff"
)

// wantedKey is a value Baton wants to own in a shared JSON file.
//
// With Item empty, the value is the member at Key (a path from the top of the
// document). With Item set, Key leads to an array, which belongs to the user
// and to other tools too, and the value is one element of it: the one that
// holds a hook handler whose command is Item. Nothing else in the array is
// ever changed.
type wantedKey struct {
	Key  []string
	Item string
	// Value is what is written, secrets included.
	Value any
	// Template is the same with ${VAR} placeholders in place of secrets,
	// which is what a question shows.
	Template any
}

// jsonTarget is one shared JSON file and the keys of it that Baton owns.
type jsonTarget struct {
	// rel is the path inside the Profile directory, as the manifest records it.
	rel string
	// backupRel names the copy in the backup directory; name is the file name
	// shown in a diff.
	backupRel string
	name      string
	wanted    []wantedKey
	// stale are the owned keys the Profile no longer receives.
	stale []OwnedKey
	// mode is the mode of the file when sync creates it.
	mode fs.FileMode
	// hasSecrets is set when a placeholder was substituted in any value: such
	// a value is never written into a file others can read.
	hasSecrets bool
	// hidden are the secrets that must not appear in what a question shows.
	hidden map[string]string
}

// planJSONKeys plans Baton's keys in a shared JSON file.
//
// The file belongs to someone else and Baton owns only the keys it recorded in
// the manifest. A file that does not exist yet is nobody's, so it is created
// without a question. Baton's keys are updated, added or removed without one
// while they are still what Baton wrote; anything else is a question, which
// shows only Baton's keys.
func (b *Bus) planJSONKeys(ctx context.Context, plan *profilePlan, previous Manifest, t jsonTarget) error {
	owned := make([]OwnedKey, 0, len(t.wanted))
	for _, w := range t.wanted {
		owned = append(owned, OwnedKey{Path: t.rel, Key: w.Key, Item: w.Item, Sum: sumOfJSON(w.Value)})
	}
	plan.manifest.Keys = append(plan.manifest.Keys, owned...)
	// While the files are changed the manifest records the new keys and those
	// about to be taken out.
	plan.keysBefore = append(append(plan.keysBefore, owned...), t.stale...)
	if len(t.wanted) == 0 && len(t.stale) == 0 {
		return nil
	}

	file, err := plan.files.QueryShared(ctx, t.rel)
	if err != nil {
		return err
	}
	if !file.Exists {
		if len(t.wanted) == 0 {
			return nil
		}
		edit, err := editKeys([]byte("{}\n"), previous, t)
		if err != nil {
			return fmt.Errorf("%s: %w", file.Path, err)
		}
		plan.shared = append(plan.shared, sharedChange{rel: t.rel, path: plan.abs(t.rel), data: edit.data, mode: t.mode})
		return nil
	}

	// Secrets are never written into a file others can read, and a file Baton
	// does not own is never chmod-ed: the user has to.
	if t.hasSecrets && file.Mode.Perm()&0o077 != 0 {
		return fmt.Errorf("%s is accessible by others (mode %04o) and would receive secrets; run: chmod 600 %s", file.Path, file.Mode.Perm(), file.Path)
	}

	edit, err := editKeys(file.Data, previous, t)
	if err != nil {
		return fmt.Errorf("%s: %w", file.Path, err)
	}
	plan.unchanged += edit.unchanged
	if !edit.changed {
		return nil
	}
	plan.shared = append(plan.shared, sharedChange{rel: t.rel, path: file.Path, data: edit.data, mode: file.Mode.Perm()})
	if edit.needsAsk {
		plan.asks = append(plan.asks, ask{
			rel:      t.backupRel,
			snapshot: snapshotOf(file),
			question: Question{
				Profile: plan.profile.Name.String(),
				Path:    plan.abs(t.rel),
				Diff:    edit.diff,
			},
		})
	}
	return nil
}

// keysEdit is the change to an existing shared JSON file.
type keysEdit struct {
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

// editKeys applies the target to the document.
func editKeys(data []byte, previous Manifest, t jsonTarget) (keysEdit, error) {
	edit := keysEdit{data: data}
	var oldView, newView strings.Builder
	var labels []string
	show := func(label string, raw []byte, found bool, template any, after string) {
		labels = append(labels, label)
		if found {
			fmt.Fprintf(&oldView, "%s = %s\n", label, hide(prettyJSON(compactJSON(coverSecrets(decodeJSON(raw), template))), t.hidden))
		} else {
			fmt.Fprintf(&oldView, "%s = (absent)\n", label)
		}
		fmt.Fprintf(&newView, "%s = %s\n", label, after)
	}

	for _, w := range t.wanted {
		raw, index, found, err := locate(edit.data, w.Key, w.Item)
		if err != nil {
			return keysEdit{}, err
		}
		if found && sumOfRaw(raw) == sumOfJSON(w.Value) {
			edit.unchanged++
			continue
		}
		recorded, owned := recordedKeySum(previous, t.rel, w.Key, w.Item)
		switch {
		case found && (!owned || sumOfRaw(raw) != recorded):
			edit.needsAsk = true // someone else's, or edited since
		case !found && owned:
			edit.needsAsk = true // cut out by hand
		case !found && !ownsAnyIn(previous, t.rel):
			edit.needsAsk = true // the first time Baton adds its part to this file
		}
		template := w.Template
		if template == nil {
			template = w.Value
		}
		show(labelOf(w.Key, w.Item), raw, found, template, prettyJSON(compactJSON(template)))
		switch {
		case w.Item == "":
			edit.data, err = jsonkeys.Set(edit.data, w.Key, compactJSON(w.Value))
		case found:
			edit.data, err = jsonkeys.SetElement(edit.data, w.Key, index, compactJSON(w.Value))
		default:
			edit.data, err = jsonkeys.AppendElement(edit.data, w.Key, compactJSON(w.Value))
		}
		if err != nil {
			return keysEdit{}, err
		}
	}

	for _, k := range t.stale {
		raw, index, found, err := locate(edit.data, k.Key, k.Item)
		if err != nil {
			return keysEdit{}, err
		}
		if !found {
			continue
		}
		if sumOfRaw(raw) != k.Sum {
			edit.needsAsk = true
		}
		show(labelOf(k.Key, k.Item), raw, found, nil, "(removed)")
		if k.Item == "" {
			edit.data, err = jsonkeys.Delete(edit.data, k.Key)
		} else {
			edit.data, err = jsonkeys.DeleteElement(edit.data, k.Key, index)
		}
		if err != nil {
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

// locate finds the value of a key: the member at key, or, with an item, the
// element of the array at key that holds the item's hook command. index is the
// position of that element.
func locate(data []byte, key []string, item string) (raw []byte, index int, found bool, err error) {
	if item == "" {
		raw, found, err = jsonkeys.Get(data, key)
		return raw, 0, found, err
	}
	elems, ok, err := jsonkeys.Elements(data, key)
	if err != nil || !ok {
		return nil, 0, false, err
	}
	i := slices.IndexFunc(elems, func(e []byte) bool { return holdsCommand(e, item) })
	if i < 0 {
		return nil, 0, false, nil
	}
	return elems[i], i, true, nil
}

// holdsCommand reports whether a matcher group of a hooks array has a handler
// with exactly this command.
func holdsCommand(group []byte, command string) bool {
	var g struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(group, &g); err != nil {
		return false
	}
	for _, h := range g.Hooks {
		if h.Command == command {
			return true
		}
	}
	return false
}

// labelOf names a key in a question.
func labelOf(key []string, item string) string {
	label := strings.Join(key, ".")
	if item != "" {
		label += " (Baton's entry)"
	}
	return label
}

// ownsAnyIn reports whether the manifest records any key in the file.
func ownsAnyIn(m Manifest, rel string) bool {
	return slices.ContainsFunc(m.Keys, func(k OwnedKey) bool { return k.Path == rel })
}

func recordedKeySum(m Manifest, rel string, key []string, item string) (string, bool) {
	for _, k := range m.Keys {
		if k.Path == rel && k.Item == item && slices.Equal(k.Key, key) {
			return k.Sum, true
		}
	}
	return "", false
}
