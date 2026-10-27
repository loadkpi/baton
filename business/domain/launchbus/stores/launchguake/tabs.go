package launchguake

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// callTimeout bounds one call to Guake, so that a Guake that does not answer
// cannot hold the caller.
const callTimeout = 5 * time.Second

// Tabs returns the title of every tab, asked of Guake over D-Bus with
// `gdbus call` (get_tab_count, then get_tab_name for each index).
func (t *Terminal) Tabs(ctx context.Context) ([]string, error) {
	out, err := remote(ctx, "get_tab_count")
	if err != nil {
		return nil, err
	}
	count, err := parseInt(out)
	if err != nil {
		return nil, fmt.Errorf("guake: cannot read the number of tabs from %q", out)
	}
	titles := make([]string, count)
	for i := range titles {
		out, err := remote(ctx, "get_tab_name", strconv.Itoa(i))
		if err != nil {
			return nil, err
		}
		if titles[i], err = parseString(out); err != nil {
			return nil, fmt.Errorf("guake: cannot read the name of tab %d from %q", i, out)
		}
	}
	return titles, nil
}

// Select shows the tab with the index and raises Guake: select_tab, then show.
func (t *Terminal) Select(ctx context.Context, index int) error {
	if _, err := remote(ctx, "select_tab", strconv.Itoa(index)); err != nil {
		return err
	}
	_, err := remote(ctx, "show")
	return err
}

// remote calls a method of Guake's D-Bus interface and returns what gdbus
// printed.
func remote(ctx context.Context, method string, params ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	args := []string{
		"call", "--session",
		"--dest", "org.guake3.RemoteControl",
		"--object-path", "/org/guake3/RemoteControl",
		"--method", "org.guake3.RemoteControl." + method,
	}
	cmd := exec.CommandContext(ctx, "gdbus", append(args, params...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("gdbus: %w: %s", err, msg)
		}
		return "", fmt.Errorf("gdbus: %w", err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// parseInt reads gdbus's `(3,)`.
func parseInt(s string) (int, error) {
	inner, ok := tuple(s)
	if !ok {
		return 0, fmt.Errorf("not a tuple: %q", s)
	}
	return strconv.Atoi(strings.TrimSpace(inner))
}

// parseString reads gdbus's `('text',)`. GVariant text quotes a string with
// single quotes, or with double quotes when it holds a single quote, and
// escapes backslash, the quote and newline.
func parseString(s string) (string, error) {
	inner, ok := tuple(s)
	if !ok {
		return "", fmt.Errorf("not a tuple: %q", s)
	}
	inner = strings.TrimSpace(inner)
	if len(inner) < 2 || (inner[0] != '\'' && inner[0] != '"') || inner[len(inner)-1] != inner[0] {
		return "", fmt.Errorf("not a string: %q", s)
	}
	var b strings.Builder
	body := inner[1 : len(inner)-1]
	for i := 0; i < len(body); i++ {
		if body[i] == '\\' && i+1 < len(body) {
			i++
			if body[i] == 'n' {
				b.WriteByte('\n')
				continue
			}
		}
		b.WriteByte(body[i])
	}
	return b.String(), nil
}

// tuple is the inside of `(x,)`.
func tuple(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "(") || !strings.HasSuffix(s, ",)") {
		return "", false
	}
	return s[1 : len(s)-2], true
}
