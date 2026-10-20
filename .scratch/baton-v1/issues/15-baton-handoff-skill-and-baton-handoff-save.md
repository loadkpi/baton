# 15: `/baton-handoff` skill and `baton handoff save`

**What to build:** a `/baton-handoff` skill, shipped with Baton as a built-in item that `baton sync` installs into every Profile, lets the Agent write the narrative part of a Handoff and save it with `baton handoff save`, before or after `baton handoff` was started. When the handing-over Session could not write it, the receiving Session does so on its first turn. The Handoff header says who wrote it.

**Blocked by:** 14

**Status:** ready-for-agent

- [ ] `baton sync` installs the built-in `/baton-handoff` skill into every host and sandbox Profile, independent of tags and of the user's source repository.
- [ ] Built-in skills are named `baton-*`; a skill named `baton-*` in the user's source makes sync stop with an error naming it.
- [ ] The skill asks for: goal; what is done; decisions and why; rejected options and why; one concrete next step; open questions; how to verify.
- [ ] `baton handoff save` reads Markdown from stdin and resolves the Task from the current directory, on the host and inside the sandbox, without environment variables.
- [ ] If the Task's latest `<n>.md` has no narrative part, `save` fills it and sets `narrative: receiving`; otherwise it writes `handoffs/<task>/draft.md`, and a later save replaces the draft.
- [ ] Running `/baton-handoff` before and after starting `baton handoff` produces the same Handoff.
- [ ] When the Handoff says `narrative: missing`, the first message of the receiving Session also asks it to write the narrative part and save it with `baton handoff save`.
