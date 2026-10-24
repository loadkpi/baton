# 26: Scrub the existing archive

**What to build:** `baton scrub [--dry-run]` applies the masking of tickets 21 and 25 to what is already on disk: Event files written before masking existed, and Handoffs, which hold command strings taken from those Events (D5).

**Blocked by:** 21, 22, 25

**Status:** open

- [ ] It goes over every Event file (host, and every Project's `sessions/`) and rewrites a file only if masking changes at least one line, with the same masking as `baton hook` (the same code, the literals of `~/.config/baton/secrets.env` read the same way, silently pattern-only when it cannot be read). A changed line gets `"masked": true` like a new one; a line that is not a valid Event (broken JSON, another version) is kept byte for byte, never masked by guesswork and never dropped. A file that needs no change is not touched (same bytes, same modification time).
- [ ] The rewrite is safe against a hook appending at the same moment: it takes the lock `baton hook` takes (ticket 22's `jsonl` lock discipline), writes a temporary file in the same directory with the original mode (0600) and renames it over the original, so a hook either lands before (and is scrubbed) or after (into the new file); no line is lost. Active Sessions are scrubbed too.
- [ ] It also masks the text of every Handoff `*.md` file (and `draft.md`) of every Project with the same Redactor, rewriting only on change, atomically, keeping the mode. The narrative a human or an Agent wrote is masked as text like any other; this is stated in `DESIGN.md`.
- [ ] Output: one line per file changed (or, with `--dry-run`, that would change; nothing is written) naming the file as `baton prune` does, then a total of files and lines changed; nothing to scrub → says so, exit 0. A file that cannot be rewritten is reported on stderr, the rest are processed, exit 1. It never prints a secret or a line of a file.
- [ ] Running it twice changes nothing the second time.
- [ ] Tested at the binary seam only (AGENTS.md): old unmasked Events with each pattern and a `secrets.env` literal (host and Project directory); a broken line and a foreign-version line survive byte for byte; an untouched file keeps bytes and mtime; mode stays 0600; a Handoff with a command carrying a secret; `--dry-run`; idempotence; a hook that waits for the lock while scrub renames the file still writes its line (as in 22's lock test); `baton ls` and `baton handoff` still work afterwards.
- [ ] `DESIGN.md` D7 (a "Scrub" paragraph: what it rewrites, what it leaves, that it cannot reach the Agents' own session files) and a README status row (`Stubs only`).
