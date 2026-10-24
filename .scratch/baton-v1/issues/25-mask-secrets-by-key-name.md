# 25: Mask secrets by the name of the JSON key

**What to build:** ticket 21 masks only what a string value looks like. A payload `{"password": "hunter2"}` or `{"api_key": "abc"}` keeps its value because the value alone matches no pattern. `baton hook` also masks the value of a string field whose key says it is a secret.

**Blocked by:** 21

**Status:** done

- [x] In the walk of `maskPayload`, a string value whose key (case-insensitive) contains `token`, `secret`, `password`, `passwd`, `api_key`, `api-key`, `apikey`, `private_key` or `private-key` is replaced whole by `[REDACTED]`, at any depth, in objects inside arrays too. Only non-empty string values: numbers (`max_tokens`, `token_count`), booleans, `null`, objects and arrays under such a key are not replaced as a whole (their string leaves are still masked by value and, if their own key says so, by key). Keys themselves are never changed.
- [x] The Event is marked `"masked": true` when a value was replaced this way, as in 21.
- [x] Known false positives are accepted and listed in `DESIGN.md` D7 (for example a field called `token_type` holding `"bearer"` is masked); do not add an allow-list in this ticket.
- [x] Tested at the binary seam only (AGENTS.md): each key word in a different case; nested in an object and in an array; a number under `max_tokens` untouched; an object under `secrets` keeps its structure with its string leaves masked; an empty string stays empty; no key match and no pattern → the line is byte for byte as before; `cwd` and `session_id` outside the payload are not touched.
- [x] `DESIGN.md` D7 updated: move this from "not masked" to masked, keep the false-positive note.
