# Claude Code hook payload fixtures

Seven of the eight files here are recordings; one is still from the docs.

**Recordings** (`SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`,
`Stop`, `SessionEnd`, `PermissionRequest`): payloads that a throwaway hook
copied from a live Claude Code 2.1.311 on Oct 27, 2026, on Linux, with a
throwaway Profile. `permission_mode` was `auto`, except in `PermissionRequest`,
where it was `default`.

**From the docs**: `PostToolUseFailure.json` is the example of the Claude Code
hooks reference (https://code.claude.com/docs/en/hooks, read on Oct 20, 2026),
copied as it is printed there. It was not seen live, so it shows what the
documentation promises, not what Claude Code sends.

**Changed by hand** in the recordings, to keep private paths out of the
repository: `cwd` is `/home/user/trial-app`, `transcript_path` and
`scratchpad_dir` are neutral paths under `/home/user` and `/tmp`, and the
session ids and tool-use ids are those of the recording.

Live payloads carry fields the docs did not show: `prompt_id`, `effort`
(`{"level": ...}`) and `scratchpad_dir`. The live `SessionStart` has no
`context_tokens` and the like. The live `Stop` carries `background_tasks` and
`session_crons` as empty lists.

Tests rewrite `session_id` and `cwd` before they pipe a payload into
`baton hook`; the other fields reach the Event file untouched. A test that
needs something a recording does not have (a secret in a command, say) builds
its own payload.

To refresh them, run a throwaway hook that copies its standard input to a file
for each Event under a live Claude Code with a throwaway Profile (settings.json
written by hand, never a real Profile), review the payloads for anything
private, and copy them over these files. DESIGN.md ("Event files and
`baton ls`") lists what else is still unverified.
