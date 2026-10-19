# 05: Host Claude Session shows up in `baton ls`

**What to build:** after `baton sync` installs Baton's status hooks into my host Claude Profiles, I start `claude-personal` by hand in any git repository and `baton ls` shows that Session with a live status. Introduces `baton hook <agent>`, per-Session Event files, automatic Project registration, and the Event → status derivation for Claude Code.

**Blocked by:** 01

**Status:** ready-for-agent

- [ ] `baton sync` installs status hooks into every host Claude Profile that call `baton hook claude` for the Events in the status table.
- [ ] `baton hook <agent>` reads the payload from stdin, adds agent, timestamp and host name, and appends exactly one line to that Session's Event file under a file lock; concurrent hooks with payloads larger than the atomic append size never interleave.
- [ ] Event files are readable only by me.
- [ ] A Session in a git repository Baton does not know yet registers that Project automatically by its git root, with the default Runtime `sandbox` if it has `.ai-sandbox/run.sh` and `host` otherwise.
- [ ] `baton ls` shows Project, Agent, Profile, Runtime and status for every Session, with statuses derived from the last Event per the Claude Code column of the status table (`running`, `waiting_approval`, `waiting_input`, `ended`).
- [ ] Tested with recorded real Claude Code hook payloads as fixtures.
