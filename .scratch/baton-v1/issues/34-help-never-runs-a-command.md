# 34: `--help` never runs a command

**What to build:** `baton sync` refuses arguments it does not know, and `-h`, `--help` and `help` print usage and exit 0 everywhere, instead of running the command or failing with an error.

**Blocked by:** none

**Status:** done

**Why.** Found on Oct 28, 2026: `baton sync --help` on the real machine ran a real sync, because `runSync` is called without the arguments (`app/baton/main.go`). It stopped only because there was no terminal to ask about a foreign file, so nothing was written; from a terminal it would have asked and then written. Every other command rejects an unknown flag, but `--help` looks different in each: `new`, `prune`, `scrub`, `handoff` print `<cmd>: flag: help requested (usage: ...)` with exit 2, `ls` and `watch` print their usage with exit 2, `session --help` says `unknown subcommand "--help"`, and `baton --help` / `baton help` say `unknown command`.

- [x] `baton sync` with any argument other than `-h`/`--help` prints `baton: usage: baton sync` on stderr, exits 2 and reads nothing (no config load, no Profile touched).
- [x] `baton -h`, `baton --help` and `baton help` print on stdout the list of commands, one per line with its usage line, and exit 0. `baton` with no argument keeps its current error and exit code.
- [x] `baton <command> -h` / `--help` prints that command's usage line(s) on stdout and exits 0, without doing anything else, for every command: `sync`, `new`, `task` (and `task name`, `task status`), `session` (and `session end`), `handoff` (and `handoff save`), `hook`, `ls`, `prune`, `scrub`, `watch` (and `watch install`), `top`, `away`, `back`. The usage text is the one the command already prints on a usage error, kept in one place per command so the two cannot drift.
- [x] Usage errors keep going to stderr with exit 2, as now.
- [x] Tests at the binary seam: `sync --help` and `sync --bogus` against a configured Profile write nothing (bytes and mtimes of the Profile and `~/.baton` unchanged); `-h`/`--help` for each command and subcommand prints a line that starts with `usage: baton <command>` on stdout with exit 0 and an empty stderr; `away --help` does not turn away mode on; `top --help` does not start the table; `baton help` lists every command.
- [x] README: one line that `baton help` and `<command> --help` exist. DESIGN.md: a short note where the commands are introduced, with the reason (ticket 34).
