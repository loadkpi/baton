# Baton

Baton is a personal orchestrator on top of the official [Claude Code](https://code.claude.com) and [Codex](https://github.com/openai/codex) CLIs. It is one Go binary with no server:

- **`baton sync`** renders one private repository of instructions, skills, MCP servers and status hooks into every Agent Profile: on the host, inside [`ai-sandbox`](https://github.com/loadkpi/ai-sandbox) containers and inside a project's Docker Compose dev container.
- **`baton new` / `baton ls`** start Sessions of either Agent in a named Guake tab and list every Session with a live status (`running`, `waiting_approval`, `waiting_input`, `ended`, `lost`), including Sessions started by hand.
- **`baton watch`** sends a desktop notification when an Agent needs you, or a one-way Telegram message while you are away.
- **`baton handoff`** moves a Task from one Agent or Profile to another without copying context by hand.

Baton never replaces the Agents and never touches their tokens: it runs the vendor CLIs under your own login and learns everything from their hooks.

## Status

Work in progress; v1 is being built ticket by ticket. Today part of `baton sync`, `baton ls`, `baton new` and Tasks work. `baton sync` handles skills, the instructions block in `CLAUDE.md`, MCP servers (with secrets from a local file) and Baton's status hooks in `settings.json` for host Claude Profiles, filtered by tags (work content goes only to the Profiles that accept it), with an owned-files manifest, a diff-and-confirm step and backups before Baton changes anything it did not create. MCP servers go to the Profile's `.claude.json` (`~/.claude.json` for the default `~/.claude` Profile, `<dir>/.claude.json` for the others). Sync refuses to put secrets into a file others can read. The status hooks call `~/.baton/bin/baton`, which `make install` puts there. After that, a Claude started by hand under any synced Profile (Sessions that were already running keep their old hooks) shows up in `baton ls` with its Project, Task, Agent, Profile, Runtime and status (`running`, `waiting_approval`, `waiting_input` or `ended`); Projects register themselves from the main checkout of the git repository of the Session's directory, and a git worktree is a Working copy of the same Project. This part is tested with payloads copied from the documentation and with stubs, not yet under a live Claude Code. `baton new <project> [task] --agent claude [--profile <name>]` makes a Task current in the Working copy and opens a Guake tab there (the Project's main checkout for a registered name, or the git repository or worktree whose path you give, which registers the Project), titled `<project>/<task>:claude`, running Claude Code under the chosen Profile with `ANTHROPIC_API_KEY` and `OPENAI_API_KEY` removed; the Profile defaults to `default_profile` or to the only Claude Profile. Without a task name it creates an unnamed Task (`2026-10-20-1`); a name continues that Task or creates it. `baton task name <project>/<task> <name>` names a Task later and `baton task status <project>/<task> <open|done|dropped>` closes it. A Session belongs to the Task that was current in its Working copy at its first Event, including a Session started by hand, and `baton new` refuses while a Session is active in the Working copy; a Session that crashed without ending blocks it until `lost` detection arrives. Only the `host` Runtime and Claude work. The Guake side is tested against a stub only, not yet against a live Guake. `lost`, `baton watch`, `baton handoff`, Codex, sandboxes and Compose are designed but not built yet.

## Build and test

Requires Go 1.24 and Linux.

```sh
make build      # static binary at bin/baton
make test       # tests, then golangci-lint
make install    # copy the binary to ~/.baton/bin/baton, where the status hooks call it
```

The tests run the built `baton` as a subprocess against a temporary `HOME`, with `guake`, `docker`, `notify-send`, `claude` and `codex` replaced by recording stubs. They never touch your real Profiles.

## Configuration

`~/.config/baton/config.toml`:

```toml
source = "~/path/to/your/agent-config"   # private git repository with skills/ and AGENTS.md

[profiles.claude-personal]
agent = "claude"
dir   = "~/.claude-personal"
tags  = ["personal"]                     # optional: tags this Profile accepts
```

Put `default_profile = "claude-personal"` before the first table to choose the Profile `baton new` uses without `--profile`.

The source repository holds `mcp.json` (Claude's `mcpServers` shape; a server may carry `"tags": ["work"]`), `skills/<name>/SKILL.md` (plus any supporting files) and `AGENTS.md` with instructions. A skill is tagged with `tags: [work]` in its front matter, a part of `AGENTS.md` by wrapping it in `<!-- baton:tags work -->` and `<!-- /baton:tags -->` lines. Untagged items go to every Profile; a tagged one only to Profiles that accept at least one of its tags. Secrets never go into the source: write `${VAR}` in `mcp.json` and put the values in `~/.config/baton/secrets.env` (`VAR=value` lines, mode 0600; `baton sync` refuses to run if others can read it). A Profile receives only the secrets of the servers it accepts. Baton records what it wrote in `~/.baton/manifests/<profile>.json` and only ever updates or deletes those files, blocks and keys. See [DESIGN.md](DESIGN.md#config-source-and-manifest-formats) for the formats.

## Documentation

- [DESIGN.md](DESIGN.md): goals, architecture and key decisions.
- [GLOSSARY.md](GLOSSARY.md): the domain vocabulary (Agent, Profile, Runtime, Session, Task, …).
- [docs/adr/](docs/adr/): decisions that are hard to reverse.
- [.scratch/baton-v1/](.scratch/baton-v1/): the v1 spec and its tickets.
- [AGENTS.md](AGENTS.md): conventions for coding agents working on this repository.

## License

[MIT](LICENSE)
