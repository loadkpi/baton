# Claude Code hook payload fixtures

FROM DOCS, REPLACE WITH RECORDINGS.

Every file here is the example payload of one Event from the Claude Code hooks
reference (https://code.claude.com/docs/en/hooks, read on Oct 20, 2026), copied
as it is printed there. None of them was recorded from a live Claude Code, so
they show what the documentation promises, not what Claude Code sends.

Tests rewrite `session_id` and `cwd` before they pipe a payload into
`baton hook`; the other fields reach the Event file untouched.

To replace them with recordings, run a throwaway hook that copies its standard
input to a file for each of these Events under a live Claude Code with a
throwaway Profile (settings.json written by hand, never a real Profile),
review the payloads for anything private, copy them over these files and update
the expected values in `test/hook_test.go` if a field changed. DESIGN.md
("Event files and `baton ls`") lists what else is still unverified. Events the
docs print no example for are not here.
