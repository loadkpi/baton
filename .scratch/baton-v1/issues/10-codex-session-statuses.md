# 10: Codex Session statuses

**What to build:** `baton new <project> --agent codex` in a sandbox Project starts Codex, and `baton ls` shows its live status with the same five statuses as Claude. If a Codex Session reports nothing, `baton ls` tells me to trust the hooks with `/hooks`.

**Blocked by:** 09

**Status:** done (live recording pending) (all but the last box: the fixtures are built from the documentation, not recorded; see `test/testdata/codex-hooks/README.md`)

- [x] Before building: check that Codex payloads carry `session_id`, `hook_event_name` and `cwd` like Claude's (they do, per the hooks page; DESIGN.md, "Codex Sessions"); if not, the hook takes these fields under Codex's names into the same line fields.
- [x] Codex Events map to statuses per the Codex column of the status table (`Stop` and `Interrupt` → `waiting_input`, `PermissionRequest` → `waiting_approval`, and so on).
- [x] `baton hook codex` finishes well within Codex's 1-second `SessionEnd` budget.
- [x] A Codex Session started by Baton that has no Events is flagged in `baton ls` with a hint to run `/hooks`.
- [x] Baton never writes Codex hook-trust state itself.
- [ ] Tested with recorded real Codex hook payloads as fixtures.
