# 04: MCP manifest with secrets

**What to build:** I declare each MCP server once in the source, with `${VAR}` placeholders instead of secrets, keep the values in my local secrets file, and `baton sync` renders the servers into each Profile's MCP configuration. A Profile gets only the secrets of the items its tags accept.

**Blocked by:** 03

**Status:** done

- [x] Before building: check whether Claude rewrites its MCP configuration (`.claude.json`) while running, so that a merge by sync can be lost. If it does, add servers through Claude's own CLI instead (`claude mcp add-json --scope user` with `CLAUDE_CONFIG_DIR` [unverified]). Record the result in `DESIGN.md` D4.
  - Checked and recorded in `DESIGN.md` D4, "MCP and secrets": Claude does rewrite `.claude.json` (docs). The file location is now verified by observation (default Profile `~/.claude.json`, relocated Profile `<dir>/.claude.json`) and implemented. Still `[unverified]`: whether a merge by sync can be lost (does Claude re-read before writing). Final call (lead): keep the file editor instead of `claude mcp add-json`, because `add-json` would put secrets into process arguments and ticket 05 needs the same flow for hooks; revisit if lost merges show up in practice.
- [x] The MCP manifest's file name and format are chosen here and recorded in `DESIGN.md` D4.
- [x] One MCP manifest in the source is rendered into the MCP configuration of each host Claude Profile that accepts it, as Baton-owned server names through the shared-file flow from 02.
- [x] `${VAR}` placeholders are replaced with values from the Baton secrets file.
- [x] A missing variable makes sync stop with an error naming the variable and the item; no partial file is written.
- [x] A secrets file readable by others makes sync refuse to run.
- [x] A Profile never receives a secret used only by items its tags do not accept.
- [x] Sync owns named keys in shared JSON/TOML files (the Claude MCP configuration, Codex `config.toml` where it applies): a `keys` array in the manifest next to `files` and `blocks`, editors that change only Baton's keys and keep every other byte, and the question/backup/hand-edit rules from 02. Moved here from 02 because this is their first user.
  - JSON editor and `keys` done. No TOML editor: no host Profile uses TOML and sandbox Codex gets fragments (D4), so it waits for a first user.
