# 14: `baton handoff` to another Agent or Profile

**What to build:** `baton handoff <task> --to <agent>[@<profile>]` waits until the Task's active Session ends, then writes the automatic part of a Handoff, joins it with the narrative draft if one was saved, and opens a new Session of the target Agent and Profile whose first message points to the Handoff. A Task moves from Claude to Codex, or between Claude Profiles, with one command.

**Blocked by:** 07, 10

**Status:** done

- [x] Before building: check whether interactive `codex` accepts an initial prompt as a positional argument, and whether `PostToolUse` payloads of both Agents carry the command's exit code. Record both in `DESIGN.md` D5.
- [x] If the Task's Session is active, `baton handoff` says it is waiting and continues only after `SessionEnd` or the Session becoming `lost`.
- [x] The automatic part is taken after the wait, so it is fresh: Task, Working copy, branch, HEAD, `git status`, `git diff --stat`, the last 10 commands with exit codes taken from Events, and a link to the Session's Event file.
- [x] If `handoffs/<task>/draft.md` exists, it becomes the narrative part and is removed; the header says `narrative: handing-over`. Otherwise the header says `narrative: missing`. (Tests write the draft file directly; `baton handoff save` comes in 15.)
- [x] Handoffs are stored as `handoffs/<task>/<n>.md` under Baton's Project directory, numbered per Task, never in the Working copy.
- [x] The new Session starts in the target Agent's Runtime with "continue the Task from Handoff `<path>`" as its first message, using the path as seen from that Runtime.
- [x] A Handoff to a sandbox Agent from a Working copy other than the Project's main checkout is refused with an error.
- [x] Tested through the `codex`/`claude` and `guake` stubs: the stub is not invoked until the test appends `SessionEnd`, and a git change made during the wait shows up in the Handoff.
