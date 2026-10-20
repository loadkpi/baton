# Baton

Baton is a personal orchestrator on top of the official [Claude Code](https://code.claude.com) and [Codex](https://github.com/openai/codex) CLIs. It is one Go binary with no server:

- **`baton sync`** renders one private repository of instructions, skills, MCP servers and status hooks into every Agent Profile: on the host, inside [`ai-sandbox`](https://github.com/loadkpi/ai-sandbox) containers and inside a project's Docker Compose dev container.
- **`baton new` / `baton ls`** start Sessions of either Agent in a named Guake tab and list every Session with a live status (`running`, `waiting_approval`, `waiting_input`, `ended`, `lost`), including Sessions started by hand.
- **`baton watch`** sends a desktop notification when an Agent needs you, or a one-way Telegram message while you are away.
- **`baton handoff`** moves a Task from one Agent or Profile to another without copying context by hand.

Baton never replaces the Agents and never touches their tokens: it runs the vendor CLIs under your own login and learns everything from their hooks.

## Status

Work in progress; v1 is being built ticket by ticket. Today only part of `baton sync` works: skills and the instructions block in `CLAUDE.md` for host Claude Profiles, filtered by tags (work content goes only to the Profiles that accept it), with an owned-files manifest, a diff-and-confirm step and backups before Baton changes anything it did not create. Everything else above is designed but not built yet.

## Build and test

Requires Go 1.24 and Linux.

```sh
make build      # static binary at bin/baton
make test       # tests, then golangci-lint
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

The source repository holds `skills/<name>/SKILL.md` (plus any supporting files) and `AGENTS.md` with instructions. A skill is tagged with `tags: [work]` in its front matter, a part of `AGENTS.md` by wrapping it in `<!-- baton:tags work -->` and `<!-- /baton:tags -->` lines. Untagged items go to every Profile; a tagged one only to Profiles that accept at least one of its tags. Baton records what it wrote in `~/.baton/manifests/<profile>.json` and only ever updates or deletes those files. See [DESIGN.md](DESIGN.md#config-source-and-manifest-formats) for the formats.

## Documentation

- [DESIGN.md](DESIGN.md): goals, architecture and key decisions.
- [GLOSSARY.md](GLOSSARY.md): the domain vocabulary (Agent, Profile, Runtime, Session, Task, …).
- [docs/adr/](docs/adr/): decisions that are hard to reverse.
- [.scratch/baton-v1/](.scratch/baton-v1/): the v1 spec and its tickets.
- [AGENTS.md](AGENTS.md): conventions for coding agents working on this repository.
