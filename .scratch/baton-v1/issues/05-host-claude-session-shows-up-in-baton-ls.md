# 05: Host Claude Session shows up in `baton ls`

**What to build:** after `baton sync` installs Baton's status hooks into my host Claude Profiles, I start `claude-personal` by hand in any git repository and `baton ls` shows that Session with a live status. Introduces `baton hook <agent>`, the Event line format, host Event files, Project registration on read, and the Event → status derivation for Claude Code.

**Blocked by:** 02

**Status:** ready-for-agent

- [ ] `baton sync` adds status hooks to `settings.json` of every host Claude Profile through the shared-file flow from 02, for the Events in the status table; each hook calls `baton hook claude --profile <name> --runtime host`.
- [ ] `baton hook <agent>` reads the payload from stdin and appends exactly one line to `~/.baton/sessions/<agent>-<session_id>.jsonl` under a file lock, with `v`, `ts`, `agent`, `profile`, `runtime`, `host`, `agent_pid`, `agent_start`, `session_id`, `event`, `cwd` and the raw `payload`. Concurrent hooks with payloads larger than the atomic append size never interleave.
- [ ] The hook does not call git, look up Projects or interpret the payload.
- [ ] `agent_pid` is the Agent's process, not a shell between it and the hook. Check the parent chain under a live Claude first and record what was found in `DESIGN.md`. `agent_start` is that process's start time from `/proc/<pid>/stat`.
- [ ] Event files are readable only by me.
- [ ] Readers, not the hook, register an unknown Project by the git root of the Session's `cwd`, under a lock, with the default Runtime `sandbox` if it has `.ai-sandbox/run.sh` and `host` otherwise; a name clash gets a suffix. A Session outside git shows Project `-`.
- [ ] `baton ls` shows Project, Agent, Profile, Runtime and status for every Session, with statuses derived from the last Event per the Claude Code column of the status table (`running`, `waiting_approval`, `waiting_input`, `ended`).
- [ ] Tested with recorded real Claude Code hook payloads as fixtures.
