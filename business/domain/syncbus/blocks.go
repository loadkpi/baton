package syncbus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/loadkpi/baton/foundation/mdblock"
	"github.com/loadkpi/baton/foundation/textdiff"
)

// instructionsFile is where the source's instructions go in a Claude Profile.
const instructionsFile = "CLAUDE.md"

// blockMarkers delimit the part of a Markdown file that is Baton's.
var blockMarkers = mdblock.Markers{Begin: "<!-- baton:begin -->", End: "<!-- baton:end -->"}

// instructionsBlock returns the body of Baton's block: the source's
// instructions without trailing blank lines. It fails if they hold a marker.
func instructionsBlock(source Source) (body string, ok bool, err error) {
	if !source.HasInstructions {
		return "", false, nil
	}
	for _, line := range strings.Split(source.Instructions, "\n") {
		if t := strings.TrimSpace(line); t == blockMarkers.Begin || t == blockMarkers.End {
			return "", false, fmt.Errorf("AGENTS.md contains the line %q, which Baton uses to mark its block", t)
		}
	}
	return strings.TrimRight(source.Instructions, "\r\n"), true, nil
}

func sumOf(body string) string {
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:])
}

func recordedSum(m Manifest, rel string) (string, bool) {
	for _, b := range m.Blocks {
		if b.Path == rel {
			return b.Sum, true
		}
	}
	return "", false
}

// planInstructions plans Baton's block in the Profile's instructions file.
//
// The file is shared: it belongs to the user and Baton owns a block in it. A
// file that does not exist yet is nobody's, so it is created without a
// question. The block Baton wrote before is updated or removed without one too,
// as long as its body is still what Baton wrote; anything else (a file that
// has no block, a block that was edited) is a question.
func (b *Bus) planInstructions(ctx context.Context, plan *profilePlan, previous Manifest, source Source) error {
	body, wanted, err := instructionsBlock(source)
	if err != nil {
		return err
	}
	rel := instructionsFile
	sum, recorded := recordedSum(previous, rel)

	if wanted {
		plan.manifest.Blocks = []OwnedBlock{{Path: rel, Sum: sumOf(body)}}
		plan.blocksBefore = plan.manifest.Blocks
	} else if recorded {
		plan.blocksBefore = previous.Blocks
	}
	if !wanted && !recorded {
		return nil
	}

	file, err := b.storer.QueryShared(ctx, plan.profile.Dir, rel)
	if err != nil {
		return err
	}
	if !file.Exists {
		if wanted {
			plan.shared = append(plan.shared, sharedChange{rel: rel, path: plan.abs(rel), data: []byte(mdblock.Append("", body, blockMarkers)), mode: 0o644})
		}
		return nil
	}

	text := string(file.Data)
	region, found, err := mdblock.Find(text, blockMarkers)
	if err != nil {
		return fmt.Errorf("%s: %w", file.Path, err)
	}

	var updated string
	needsAsk := true
	switch {
	case wanted && !found:
		updated = mdblock.Append(text, body, blockMarkers)
	case wanted && region.Body == body:
		plan.unchanged++
		return nil
	case wanted:
		updated = mdblock.Replace(text, region, body, blockMarkers)
		needsAsk = !recorded || sumOf(region.Body) != sum
	case !found:
		return nil // the block is gone already
	default:
		updated = mdblock.Remove(text, region)
		needsAsk = sumOf(region.Body) != sum
	}

	change := sharedChange{rel: rel, path: file.Path, data: []byte(updated), mode: file.Mode.Perm()}
	if updated == "" && !file.Linked {
		change.remove = true // the file held nothing but Baton's block
	}
	plan.shared = append(plan.shared, change)

	if needsAsk {
		plan.asks = append(plan.asks, ask{
			rel:  rel,
			path: file.Path,
			question: Question{
				Profile: plan.profile.Name.String(),
				Path:    plan.abs(rel),
				Diff:    textdiff.Unified("a/"+rel, "b/"+rel, text, updated),
			},
		})
	}
	return nil
}
