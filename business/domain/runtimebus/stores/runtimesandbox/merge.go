package runtimesandbox

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/loadkpi/baton/foundation/atomicfile"
	"github.com/loadkpi/baton/foundation/mdblock"
)

// The markers around Baton's part of the files ai-sandbox renders. They are
// Baton's own, never ai-sandbox's: run.sh rewrites the whole file (and so
// removes the block) unless the user took its managed-by marker out.
var (
	tomlMarkers = mdblock.Markers{Begin: "# baton:begin", End: "# baton:end"}
	textMarkers = mdblock.Markers{Begin: "<!-- baton:begin -->", End: "<!-- baton:end -->"}
)

// mergeFragments puts Baton's fragments, which `baton sync` wrote, between
// Baton's markers in the Codex config and instructions that run.sh rendered in
// codexDir. Running it again changes nothing, and a fragment that is gone takes
// its block with it.
func mergeFragments(codexDir string) error {
	// The MCP fragment holds secrets, so a config that carries it is private.
	if err := mergeFragment(filepath.Join(codexDir, "config.toml"), filepath.Join(codexDir, "config.d", "baton.toml"), tomlMarkers, 0o600); err != nil {
		return err
	}
	return mergeFragment(filepath.Join(codexDir, "AGENTS.md"), filepath.Join(codexDir, "AGENTS.d", "baton.md"), textMarkers, 0)
}

// mergeFragment makes the block of target hold exactly the fragment: added at
// the end when there is none yet, replaced in place when there is, removed when
// the fragment is gone or empty. A target that does not exist is left alone;
// run.sh makes it. A new block makes the file private when private is not 0.
func mergeFragment(target, fragment string, m mdblock.Markers, private fs.FileMode) error {
	body, err := os.ReadFile(fragment)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	wanted := strings.TrimRight(string(body), "\n")

	info, err := os.Stat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	current, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	text := string(current)

	region, found, err := mdblock.Find(text, m)
	if err != nil {
		return fmt.Errorf("%s: %w; make it one %q ... %q block or remove the lines", target, err, m.Begin, m.End)
	}
	if containsLine(wanted, m) {
		return fmt.Errorf("%s: the fragment holds a line Baton uses to mark its block", fragment)
	}

	var next string
	switch {
	case strings.TrimSpace(wanted) == "" && !found:
		return nil
	case strings.TrimSpace(wanted) == "":
		next = mdblock.Remove(text, region)
	case found:
		next = mdblock.Replace(text, region, wanted, m)
	default:
		next = mdblock.Append(text, wanted, m)
	}

	mode := info.Mode().Perm()
	if private != 0 && strings.TrimSpace(wanted) != "" {
		mode = private
	}
	_, err = atomicfile.WriteIfChanged(target, []byte(next), mode)
	return err
}

func containsLine(text string, m mdblock.Markers) bool {
	for _, line := range strings.Split(text, "\n") {
		if t := strings.TrimSpace(line); t == m.Begin || t == m.End {
			return true
		}
	}
	return false
}
