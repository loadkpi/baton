# 30: A Codex gone while waiting for input has ended

**What to build:** a Codex Session whose Agent is gone while it was waiting for input is `ended`, not `lost`, and `baton watch` does not notify it.

**Blocked by:** 11, 27

**Status:** done

**Why (found in ticket 27).** Codex 0.162.1 in an `ai-sandbox` container, all eight hooks trusted, quit with `/exit`: the Event file ends with `Stop`, no `SessionEnd` came, and Codex's own log (`logs_2.sqlite`) shows no hook started after that `Stop`. So the Session became `lost` and the watcher sent "session lost" for a normal exit, which will happen after every Codex Session. That Codex does not run `SessionEnd` on `/exit` is read from its log, and it was seen in a sandbox only (the host has no Codex) [unverified on the host]. Claude Code sent `SessionEnd` on `/exit` every time.

- [x] For a Codex Session (any Runtime) whose newest Event is a `Stop` or an `Interrupt` (the Events that make it `waiting_input`) and whose Agent the Runtime says is gone, the status is `ended`. Nothing is written to the Event file: like `lost`, it is decided on every read (`sessionbus.List`), so a `SessionEnd` that arrives later still simply wins. `Since` stays the time of that Event.
- [x] A Codex Session that is gone in any other state (`running`, `waiting_approval`) is still `lost`, and so is a Claude Session in every state (Claude sends `SessionEnd`).
- [x] Being `ended`, such a Session is not active (the Working copy is free for `baton new` and `baton handoff`), and `baton watch` sends nothing for it, also when it saw the Session as `waiting_input` on the previous look.
- [x] Tested at the binary seam for the host and the sandbox Runtime (Compose goes through the same rule; one test is enough if it shares the code path): Codex gone after `Stop` → `ended` in `baton ls` and no notification; Codex gone after `Interrupt` → `ended`; Codex gone while `running` → `lost` and notified; Claude gone after `Stop` → `lost` as today; a late `SessionEnd` changes nothing visible.
- [x] DESIGN.md: the rule next to "What counts as lost" (D6/Lost detection), with the evidence above, and the cost: a Codex that crashes while idle at its prompt is shown as `ended` and not notified.
