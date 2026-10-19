# 04: MCP manifest with secrets

**What to build:** I declare each MCP server once in the source, with `${VAR}` placeholders instead of secrets, keep the values in my local secrets file, and `baton sync` renders the servers into each Profile's MCP configuration. A Profile gets only the secrets of the items its tags accept.

**Blocked by:** 03

**Status:** ready-for-agent

- [ ] One MCP manifest in the source is rendered into the MCP configuration of each host Claude Profile that accepts it.
- [ ] `${VAR}` placeholders are replaced with values from the Baton secrets file.
- [ ] A missing variable makes sync stop with an error naming the variable and the item; no partial file is written.
- [ ] A secrets file readable by others makes sync refuse to run.
- [ ] A Profile never receives a secret used only by items its tags do not accept.
