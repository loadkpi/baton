# 04: MCP manifest with secrets

**What to build:** I declare each MCP server once in the source, with `${VAR}` placeholders instead of secrets, keep the values in my local secrets file, and `baton sync` renders the servers into each Profile's MCP configuration. A Profile gets only the secrets of the items its tags accept.

**Blocked by:** 03

**Status:** ready-for-agent

- [ ] Before building: check whether Claude rewrites its MCP configuration (`.claude.json`) while running, so that a merge by sync can be lost. If it does, add servers through Claude's own CLI instead (`claude mcp add-json --scope user` with `CLAUDE_CONFIG_DIR` [unverified]). Record the result in `DESIGN.md` D4.
- [ ] The MCP manifest's file name and format are chosen here and recorded in `DESIGN.md` D4.
- [ ] One MCP manifest in the source is rendered into the MCP configuration of each host Claude Profile that accepts it, as Baton-owned server names through the shared-file flow from 02.
- [ ] `${VAR}` placeholders are replaced with values from the Baton secrets file.
- [ ] A missing variable makes sync stop with an error naming the variable and the item; no partial file is written.
- [ ] A secrets file readable by others makes sync refuse to run.
- [ ] A Profile never receives a secret used only by items its tags do not accept.
