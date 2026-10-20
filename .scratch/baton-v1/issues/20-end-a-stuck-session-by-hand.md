# 20: End a stuck Session by hand

**What to build:** when Baton cannot tell that a Session is dead, I can end it myself. `baton session end <session>` records that the Session is over, so it no longer counts as active, no longer blocks its Working copy, and `baton handoff` stops waiting for it.

Found on real Docker: a Session whose recorded `agent_pid` is only the hook's parent (`agent_pid_fallback: true`, the Agent was not found among the ancestors) is never judged `lost`, by design (ticket 11), so if its Agent dies without `SessionEnd` it blocks its Working copy forever and nothing can clear it. The same holds for a Session whose Events carry no PID, or whose Runtime cannot answer (docker down).

**Blocked by:** 11

**Status:** ready-for-agent

- [ ] `baton session end <session>` takes the Session id as `baton ls` shows it (the SESSION column prefix) or in full; an ambiguous or unknown prefix is refused and lists the candidates.
- [ ] It appends one Event line to the Session's own Event file under the same file lock as `baton hook`, marked as written by Baton and not by the Agent (e.g. `event: "SessionEnd"`, `source: "baton"`, `payload` with the reason `ended by hand`), so the archive stays raw and the origin is visible.
- [ ] After it, the Session's status is `ended`, it is not active, `baton new` in its Working copy is allowed, and a waiting `baton handoff` proceeds.
- [ ] It refuses, without `--force`, a Session that Baton can see is still running (its PID is alive per the Runtime's `Alive`), naming what it saw; `--force` ends it anyway. It never kills any process.
- [ ] Works for host, sandbox and compose Sessions (the Event file of a container Session lives under `~/.baton/projects/<project>/sessions/` on the host).
- [ ] The refusal of `baton new` for an active Session, and `baton handoff` while waiting, mention `baton session end <id>` when the blocking Session cannot be judged (fallback PID, no PID, or Runtime unanswered).
- [ ] Tested at the binary seam with a fallback-PID Session that blocks `baton new`, a live-PID Session refused without `--force`, and a waiting `baton handoff` released by it.
