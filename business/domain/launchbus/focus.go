package launchbus

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrNoTab is what FocusTab answers when the Terminal has no tab with any of
// the titles.
var ErrNoTab = errors.New("no tab with that title")

// TabSwitcher is a Terminal that can list its tabs and bring one to the
// front.
type TabSwitcher interface {
	// Tabs returns the title of every tab, in the order Select numbers them.
	Tabs(ctx context.Context) ([]string, error)
	// Select shows the tab with the given index and raises the Terminal.
	Select(ctx context.Context, index int) error
}

// FocusTab brings to the front the first tab whose title is one of titles. A
// Session started by hand has no title Baton knows, and a tab renamed or closed
// since is not found: both answer ErrNoTab.
func FocusTab(ctx context.Context, sw TabSwitcher, titles []string) error {
	if len(titles) == 0 {
		return ErrNoTab
	}
	tabs, err := sw.Tabs(ctx)
	if err != nil {
		return err
	}
	for i, tab := range tabs {
		for _, title := range titles {
			if tab == title {
				return sw.Select(ctx, i)
			}
		}
	}
	return fmt.Errorf("%w: %s", ErrNoTab, strings.Join(titles, " or "))
}
