# 15: `/handoff` skill and `baton handoff save`

**What to build:** a `/handoff` skill, shipped with Baton as a built-in item that `baton sync` installs into every Profile, lets the Agent write the narrative part of a Handoff and save it with `baton handoff save`. When the handing-over Session could not write it, the receiving Session does so on its first turn. The Handoff header says who wrote it.

**Blocked by:** 14, 01

**Status:** ready-for-agent

- [ ] `baton sync` installs the built-in `/handoff` skill into every host and sandbox Profile, independent of tags and of the user's source repository.
- [ ] The skill asks for: goal; what is done; decisions and why; rejected options and why; one concrete next step; open questions; how to verify.
- [ ] `baton handoff save` reads Markdown from stdin and attaches it to the Task resolved from the current directory, on the host and inside the sandbox, without environment variables.
- [ ] The first message of the receiving Session asks it to write the narrative part if the Handoff has none.
- [ ] The Handoff header records whether the narrative part came from the handing-over or the receiving Session.
