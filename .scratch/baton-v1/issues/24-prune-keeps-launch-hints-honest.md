# 24: Prune must not make a launch look silent

**What to build:** after `baton prune`, `baton ls` must not show a `no_events` row and the "run /hooks" hint for a launch that had been heard from before. Found while reviewing ticket 22: a launch record (ticket 10) counts as heard when some Session of the same Project and Agent has an Event at or after it (`sessionbus.Silent`); when prune deletes the Event files that proved it, the old launch record becomes "silent" and `baton ls` tells me to run `/hooks` for a Session that worked. This was read from the code, not tried.

**Blocked by:** 22

**Status:** open

- [ ] First reproduce it at the binary seam: a Codex launch record, Events of a Session of that Project and Agent after it, the Session ended long ago; `baton ls` shows no `no_events`; `baton prune --older-than 1d` deletes the file; `baton ls` now shows `no_events` (red test), or, if it does not, say so and close the ticket with the test as a guard.
- [ ] Fix: prune must not turn a launch that was heard from into a silent one. A launch that was already silent before the prune stays silent and stays on disk (its hint is still true). Choose the simplest way that keeps both rules and record it in `DESIGN.md` (D7 retention subsection and the Codex part that says there is no expiry): for example, removing the launch records that the prune would newly make silent, with the same lock discipline as other launch writes.
- [ ] `--dry-run` deletes nothing, launch records included, and its output stays as it is (the launch records removed are mentioned in a line of the real run only, e.g. `removed N launch records`; the line is absent when there are none).
- [ ] Tested at the binary seam only (AGENTS.md): the reproduction above; a launch never heard from stays flagged after a prune; a launch heard from by a Session that is not pruned (young) is untouched; `--dry-run`; host and a Project's directory.
- [ ] `DESIGN.md` updated (the sentence in ticket 22's "what is not deleted" about launch records, which this ticket changes).
