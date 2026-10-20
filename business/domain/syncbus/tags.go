package syncbus

import (
	"errors"
	"fmt"
	"strings"

	"github.com/loadkpi/baton/business/types/tag"
)

// The source's AGENTS.md carries tags on parts of it. A part is wrapped in two
// whole lines, which are HTML comments, so the file reads the same everywhere
// else:
//
//	<!-- baton:tags work, family -->
//	...text for Profiles that accept work or family...
//	<!-- /baton:tags -->
//
// Text outside any part is untagged and goes to every Profile.
const (
	tagsOpenPrefix = "<!-- baton:tags "
	tagsOpenSuffix = " -->"
	tagsClose      = "<!-- /baton:tags -->"
)

// renderForTags returns the instructions a Profile that accepts the given tags
// receives: the untagged text, and the parts that carry at least one accepted
// tag. The marker lines themselves are left out. When a part is left out
// between two blank lines, one of them goes with it.
func renderForTags(text string, accepted []tag.Tag) (string, error) {
	var (
		out        strings.Builder
		inPart     bool
		keep       bool
		openedAt   int
		lastBlank  = true // the start of the text counts as a blank line
		trimBlank  bool   // a part was just left out after a blank line
		dropped    bool   // inside a part that is left out
		lineNumber int
	)
	for _, line := range strings.SplitAfter(text, "\n") {
		lineNumber++
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trimmed, tagsOpenPrefix) && strings.HasSuffix(trimmed, tagsOpenSuffix):
			if inPart {
				return "", fmt.Errorf("AGENTS.md line %d: a tags part inside the tags part opened on line %d (parts do not nest)", lineNumber, openedAt)
			}
			itemTags, err := parseTagList(strings.TrimSuffix(strings.TrimPrefix(trimmed, tagsOpenPrefix), tagsOpenSuffix))
			if err != nil {
				return "", fmt.Errorf("AGENTS.md line %d: %w", lineNumber, err)
			}
			inPart, openedAt = true, lineNumber
			keep = tag.AcceptedBy(itemTags, accepted)
			dropped = !keep
			continue
		case trimmed == tagsClose:
			if !inPart {
				return "", fmt.Errorf("AGENTS.md line %d: %q without a matching %q", lineNumber, tagsClose, tagsOpenPrefix+"... -->")
			}
			inPart = false
			if dropped {
				trimBlank = lastBlank
			}
			dropped = false
			continue
		case dropped:
			continue
		}

		blank := trimmed == ""
		if trimBlank && blank {
			trimBlank = false
			continue
		}
		trimBlank = false
		out.WriteString(line)
		lastBlank = blank
	}
	if inPart {
		return "", fmt.Errorf("AGENTS.md line %d: the tags part is never closed by %q", openedAt, tagsClose)
	}
	return out.String(), nil
}

// parseTagList reads "work, family": comma-separated tags.
func parseTagList(s string) ([]tag.Tag, error) {
	var list []string
	for _, item := range strings.Split(s, ",") {
		list = append(list, strings.TrimSpace(item))
	}
	if strings.TrimSpace(s) == "" {
		return nil, errors.New("a tags part needs at least one tag")
	}
	return tag.ParseAll(list)
}
