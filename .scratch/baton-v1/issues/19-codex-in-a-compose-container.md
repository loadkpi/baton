# 19: Codex in a Compose container

**What to build:** a Project's Compose dev container can also hold a Codex Profile. `baton sync` renders instructions, skills, MCP servers and status hooks into it like into a host Profile, `baton new --agent codex` starts Codex there, and its Sessions show up in `baton ls`. Ticket 16 supports only Claude in compose, because Codex keeps MCP servers in `config.toml` and Baton has no TOML key editor yet.

**Blocked by:** 18

**Status:** ready-for-agent

- [ ] Sync owns named keys in a shared TOML file (`mcp_servers.<name>` in Codex `config.toml`): an editor that changes only Baton's tables and keeps every other byte, the `keys` manifest entries, and the question/backup/hand-edit rules from 02, as the JSON editor from 04 does.
- [ ] A compose Profile with `agent = "codex"` is accepted. It gets skills (in the Codex skills directory of the container user, as ticket 08 found for the sandbox), instructions in `AGENTS.md` as a marked block, MCP servers through the TOML editor, and `hooks.json` with the guarded hook command (`--runtime compose`). There is no fragment treatment, because nothing regenerates these files.
- [ ] Before building: check how `CODEX_HOME` is set in the container (environment of the service or the image) and resolve the real directory inside the container, as ticket 16 does for Claude.
- [ ] `baton new <project> --agent codex` starts Codex in the service through the compose Runtime from 17, with the `/hooks` hint from 10 while it reports nothing.
- [ ] `lost` and Handoff work for compose Codex Sessions through the same code as for Claude (18).
- [ ] Tested through the fake docker from 16; the TOML editor also with byte-preservation cases (comments, ordering, inline tables).
