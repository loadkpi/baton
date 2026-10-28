# 33: Sync says that running Claude Sessions pick up its hooks

**What to build:** the note `baton sync` prints when it first installs status hooks into a Claude Profile says that Sessions already running pick them up, instead of telling the user to restart them.

**Blocked by:** none

**Status:** done

**Why.** Today the first sync into any Profile prints `<profile>: status hooks installed; Sessions of this Profile that are already running keep their old hooks and must be restarted to report Events`. For Claude Code this is wrong, and it makes the user restart Sessions for nothing. Checked on Oct 28, 2026:

- *User settings (the file sync writes).* On Oct 27 the first real sync wrote the manifests of two Claude Profiles at 15:44:25Z. Two Sessions that were already running (their Event files have no `SessionStart`) wrote their first Events 4 s and 21 s later (`UserPromptSubmit` in one Profile, `PreToolUse` in the other), Claude Code 2.1.311.
- *Project settings, controlled.* Claude Code 2.1.312, `claude -p` in a throwaway Project told to run `sleep 25` then `echo second`: with a `PreToolUse` hook in `.claude/settings.json` from the start both calls hit it; with the hook written into the file during the `sleep`, `echo second` hit it.

Whether Codex re-reads `hooks.json` in a running Session is not known, so its note stays as it is.

- [x] For a Claude Profile (every Runtime) the note is `<profile>: status hooks installed; Claude Sessions of this Profile that are already running pick them up without a restart`. It is still printed only on the first sync that adds the hooks, as now.
- [x] For a Codex Profile the note is unchanged: the restart sentence and `; Codex runs hooks only after you review and trust them with /hooks`.
- [x] Tests at the binary seam (`test/hooks_sync_test.go`): the Claude wording for a host, a sandbox and a Compose Claude Profile, the old wording for a Codex Profile, and still no note on the second sync.
- [x] DESIGN.md: the hooks section (the paragraph "What is not covered", and the Compose "Hooks" paragraph that mentions the note) says what each Agent is told and why, with the evidence above; the comment in `syncbus.go` that says running Sessions keep their old hooks is corrected for Claude. Ticket 17's line about restarting stays as history.
