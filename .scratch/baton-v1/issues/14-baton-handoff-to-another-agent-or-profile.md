# 14: `baton handoff` to another Agent or Profile

**What to build:** `baton handoff <task> --to <agent>[@<profile>]` writes the automatic part of a Handoff, waits until the Task's active Session ends, and opens a new Session of the target Agent and Profile whose first message points to the Handoff. A Task moves from Claude to Codex, or between Claude Profiles, with one command.

**Blocked by:** 07, 10

**Status:** ready-for-agent

- [ ] The automatic part contains the Task, Working copy, branch, HEAD, `git status`, `git diff --stat`, the last 10 commands with exit codes taken from Events, and a link to the Session's Event file.
- [ ] Handoffs are stored under Baton's Project directory, numbered per Task, never in the Working copy.
- [ ] If the Task's Session is active, `baton handoff` says it is waiting and opens the new Session only after `SessionEnd` or the Session becoming `lost`.
- [ ] The new Session starts in the target Agent's Runtime with "continue the Task from Handoff `<path>`" as its first message, using the path as seen from that Runtime.
- [ ] Tested through the `codex`/`claude` and `guake` stubs: the stub is not invoked until the test appends `SessionEnd`.
