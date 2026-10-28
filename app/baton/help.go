package main

import "strings"

// Usage lines of the commands that have no flags to parse. Every command keeps
// its usage in one constant, used by its usage errors and by --help alike.
const (
	syncUsage = "usage: baton sync"
	lsUsage   = "usage: baton ls [--long]"
	awayUsage = "usage: baton away"
	backUsage = "usage: baton back"
	hookUsage = "usage: baton hook <agent> --profile <name> --runtime <host|sandbox|compose>"
)

// usages are the usage lines of each command, in the order `baton help` lists
// them. A command with subcommands lists one line for each of them.
var usages = []struct {
	command string
	lines   []string
}{
	{"sync", []string{syncUsage}},
	{"new", []string{newUsage}},
	{"task", []string{taskNameUsage, taskStatusUsage}},
	{"session", []string{sessionEndUsage}},
	{"handoff", []string{handoffUsage, handoffSaveUsage}},
	{"hook", []string{hookUsage}},
	{"ls", []string{lsUsage}},
	{"prune", []string{pruneUsage}},
	{"scrub", []string{scrubUsage}},
	{"watch", []string{watchUsage}},
	{"top", []string{topUsage}},
	{"away", []string{awayUsage}},
	{"back", []string{backUsage}},
}

// subcommandUsages are the usage lines of `baton <command> <subcommand>`, keyed
// by "<command> <subcommand>".
var subcommandUsages = map[string]string{
	"task name":     taskNameUsage,
	"task status":   taskStatusUsage,
	"session end":   sessionEndUsage,
	"handoff save":  handoffSaveUsage,
	"watch install": watchUsage,
}

func isHelpFlag(s string) bool {
	return s == "-h" || s == "--help"
}

// commandNames lists the commands for the errors that name them.
func commandNames() string {
	names := make([]string, len(usages))
	for i, u := range usages {
		names[i] = u.command
	}
	return strings.Join(names, ", ")
}

// runHelp implements `baton help [command]`, `baton -h` and `baton --help`.
func runHelp(c console, words []string) int {
	if len(words) == 0 {
		for _, u := range usages {
			for _, line := range u.lines {
				c.println(line)
			}
		}
		return exitOK
	}
	if lines := usageOf(words); lines != nil {
		for _, line := range lines {
			c.println(line)
		}
		return exitOK
	}
	return c.usagef("help: unknown command %q (available: %s)", strings.Join(words, " "), commandNames())
}

// helpRequested reports whether a command line asks for help: a command
// followed, anywhere, by -h or --help. It is decided before the command reads
// anything, so asking never runs it.
func helpRequested(args []string) bool {
	for _, a := range args[1:] {
		if isHelpFlag(a) {
			return true
		}
	}
	return false
}

// usageOf returns the usage lines of the command, or of its subcommand when
// the words name one; nil when they name neither.
func usageOf(words []string) []string {
	if len(words) >= 2 {
		if u, ok := subcommandUsages[words[0]+" "+words[1]]; ok {
			return []string{u}
		}
	}
	for _, u := range usages {
		if u.command == words[0] {
			return u.lines
		}
	}
	return nil
}
