# 10: Codex Session statuses

**What to build:** `baton new <project> --agent codex` in a sandbox Project starts Codex, and `baton ls` shows its live status with the same five statuses as Claude. If a Codex Session reports nothing, `baton ls` tells me to trust the hooks with `/hooks`.

**Blocked by:** 09

**Status:** ready-for-agent

- [ ] Before building: check that Codex payloads carry `session_id`, `hook_event_name` and `cwd` like Claude's [unverified]; if not, the hook takes these fields under Codex's names into the same line fields.
- [ ] Codex Events map to statuses per the Codex column of the status table (`Stop` and `Interrupt` → `waiting_input`, `PermissionRequest` → `waiting_approval`, and so on).
- [ ] `baton hook codex` finishes well within Codex's 1-second `SessionEnd` budget.
- [ ] A Codex Session started by Baton that has no Events is flagged in `baton ls` with a hint to run `/hooks`.
- [ ] Baton never writes Codex hook-trust state itself.
- [ ] Tested with recorded real Codex hook payloads as fixtures.
