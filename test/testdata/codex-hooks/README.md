# Codex hook payload fixtures

FROM DOCS, REPLACE WITH RECORDINGS.

None of these was recorded from a live Codex. They come from the Codex hooks
reference (https://learn.chatgpt.com/docs/hooks, read on Oct 20, 2026), which
prints exactly one stdin example, and that one is only partly a fixture:

- `SessionEnd.json` is that example, copied as it is printed.
- Every other file is built from the page's tables of fields: the common fields
  (`session_id`, `transcript_path`, `cwd`, `hook_event_name`, `model`), `turn_id`
  and `permission_mode` where the page says an Event carries them, and the
  fields the page lists for that Event (`source`, `prompt`, `tool_name`,
  `tool_use_id`, `tool_input`, `tool_response`, `stop_hook_active`,
  `last_assistant_message`). The field names are the documentation's; every
  value is made up, and `tool_input` and `tool_response` have no documented
  example, so their shape here is a guess.

Tests rewrite `session_id` and `cwd` before they pipe a payload into
`baton hook`; the other fields reach the Event file untouched.

To replace them with recordings, run a throwaway hook that copies its standard
input to a file for each of these Events under a live Codex with a throwaway
Profile (never a real one; the hook has to be trusted with `/hooks` there),
review the payloads for anything private, copy them over these files and update
the expected values in `test/codex_test.go` if a field changed.
