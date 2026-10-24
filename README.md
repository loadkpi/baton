# Baton

Baton is a personal orchestrator on top of the official [Claude Code](https://code.claude.com) and [Codex](https://github.com/openai/codex) CLIs. It is one Go binary with no server:

- **`baton sync`** renders one private repository of instructions, skills, MCP servers and status hooks into every Agent Profile: on the host, inside [`ai-sandbox`](https://github.com/loadkpi/ai-sandbox) containers and inside a project's Docker Compose dev container.
- **`baton new` / `baton ls`** start Sessions of either Agent in a named Guake tab and list every Session with a live status (`running`, `waiting_approval`, `waiting_input`, `ended`, `lost`), including Sessions started by hand.
- **`baton watch`** sends a desktop notification when an Agent needs you, or a one-way Telegram message while you are away.
- **`baton handoff`** moves a Task from one Agent or Profile to another without copying context by hand.

Baton never replaces the Agents and never touches their tokens: it runs the vendor CLIs under your own login and learns everything from their hooks.

## Status

Work in progress: v1 is built ticket by ticket ([.scratch/baton-v1/issues](.scratch/baton-v1/issues)). Every feature below is covered by tests that run the `baton` binary against an isolated `HOME`, with Guake, Docker, `notify-send`, `systemctl`, Claude Code, Codex and Telegram replaced by stubs or a fake server. The last column says what was also checked for real.

| Command | What works | Checked for real |
| --- | --- | --- |
| `baton sync` | Skills, instructions (`CLAUDE.md` block / Codex `AGENTS.md` fragment), MCP servers with secrets from `secrets.env`, and status hooks, filtered by tags, into host Claude Profiles, into both Profiles of every `ai-sandbox` Project (`<project>.claude`, `<project>.codex`) and into the Claude and Codex Profiles inside a Project's Docker Compose service (running or stopped, through `docker`; a service that cannot be reached fails only its own Profiles; Codex `config.toml` is edited table by table, everything else in it stays). Owns only what it wrote (manifest); asks with a diff and backs up before changing anything else; never writes git-tracked files or secrets into files others can read. | Real Docker: synced content read by Claude Code 2.1.296 and Codex 0.162.1 inside an `ai-sandbox` container; a Compose service's Claude and Codex Profiles written in a named volume, running and stopped (owner, no temp files, removal, `codex mcp list` reading Baton's `config.toml`). |
| `baton hook` | Appends each Agent Event to a per-Session file (`~/.baton/sessions/`, or the Project's directory for a sandbox). Inside a container without Baton (sandbox or Compose) it does nothing. | Real Docker: fired by a real Claude Code in a container; silent without Baton's mounts; records the container id from `/proc/self/mountinfo` in a Compose container. |
| `baton ls` | Every Session with Project, Task, Agent, the start of its session id (8 characters, more when two collide), Profile, Runtime and status (`running`, `waiting_approval`, `waiting_input`, `ended`, `lost`); Sessions started by hand included, also in a Docker Compose container (the Project and Working copy come from the container's mounts, and `lost` is found as in a sandbox, also for a service with its own `hostname:`); a Codex Session that reported nothing is shown with a hint to run `/hooks`. | Real Docker: `lost` for a gone container, an Agent killed in a running container (also a zombie), a recreated Compose container and one with its own `hostname:`; the Codex `/hooks` hint in a Compose container. |
| `baton new` | Opens a Guake tab titled `<project>/<task>:<agent>` with Claude on the host (chosen Profile, API keys removed), Claude/Codex in the Project's `ai-sandbox` (via its `run.sh`, two Baton mounts, Codex fragments merged) or Claude/Codex in the Project's Docker Compose service (`docker compose up -d` if it is stopped, then `exec` in the Project's directory in the container; refuses with the snippet for your override file when the two Baton mounts are missing). One active Session per Working copy, whichever Runtime. | Real Docker: the sandbox `docker run` and the Compose `exec` lines (mounts, missing-mount refusal with the snippet, API keys cleared, `CODEX_HOME`). Not yet in a live Guake. |
| `baton session end` | `baton session end <session> [--force]` ends a Session Baton cannot tell is dead (the Agent died without `SessionEnd` and its process cannot be judged): it appends a `SessionEnd` Event marked as Baton's own, so the Session no longer blocks `baton new` or `baton handoff`. `<session>` is the SESSION column of `baton ls` or the whole id. Refuses a Session it sees running unless `--force`; never kills a process. `baton new` and `baton handoff` name it when they are stuck on such a Session. | Stubs only. |
| `baton prune` | `baton prune --older-than <N>d [--dry-run]` deletes the Event files of Sessions that ended or were lost more than N days ago (host, sandbox and Compose), after taking the lock the hook takes; lists each file and a total. Handoffs, Tasks and the rest stay; nothing prunes by itself. | Stubs only. |
| `baton scrub` | `baton scrub [--dry-run]` masks the secrets in the Event files and Handoffs already on disk, the way `baton hook` masks a new Event (same patterns, same `secrets.env`); rewrites only files that change, under the hook's lock, keeping mode 0600; lists each file and a total. Lines that are not Events are kept byte for byte; the Agents' own session files are out of reach. | Stubs only. |
| `baton task name/status` | Name an unnamed Task, mark it `open`, `done` or `dropped`. | — |
| `baton watch`, `watch install` | Desktop notifications for approvals, long turns and lost Sessions (also in a sandbox or Compose container, with one `docker exec` per container per look); a `systemd --user` unit. | Not yet on a real desktop or user manager. The optional `[watch] sound`: Baton's own run was checked only against stubs; a real `pw-play` was run by hand on the file `freedesktop/stereo/message-new-instant.oga` (Cinnamon 6.0.4, desktop event sounds off) and the sound was heard; `paplay` was not tried. |
| `baton away` / `back` | Notifications go to Telegram (Bot API `sendMessage`, one-way) while away. | Request shape checked against the Bot API docs; never sent to real Telegram. |
| `baton handoff` | `baton handoff <project>/<task> --to <agent>[@<profile>]` waits (saying so) until the Task's Session ends or is lost, writes a Handoff (git state, last 10 commands, Event file link, the saved narrative if any) to `~/.baton/projects/<project>/handoffs/` and opens a Session of the other Agent or Profile whose first message points to it (and asks it to write the narrative when there is none). In a Project with a Compose service the receiver is a Claude Profile of that service, started through `docker compose exec`, and the Handoff is named by its path in the container; from a Working copy other than the main checkout it is refused (as for a sandbox). | Not yet: neither Agent's TUI was started with the prompt, and exit codes are `?` for Codex and inferred for Claude (see DESIGN.md, D5). |
| `baton handoff save`, `/baton-handoff` | `baton sync` installs the built-in `/baton-handoff` skill into every Profile (host, sandbox and Compose, any tags; `baton-*` is reserved). It has the Agent write the narrative and pipe it to `baton handoff save`, which finds the Task from the current directory on the host, and through the mounted Project directory in a sandbox or a Compose container, and keeps it as a draft, or fills the latest Handoff that has none. Before or after `baton handoff` gives the same Handoff. | Real Docker: `baton handoff save` inside a Compose container. Not yet: the skill run by a real Agent. |

`baton watch` and `baton handoff` with a Compose container are tested only against the fake `docker`. A Task cannot be handed from a sandbox or Compose Session to a host Profile, only to a Profile of the same container Runtime. Not recorded yet: real hook payloads of a full interactive Session (the test fixtures come from the vendors' documentation); not tried yet: a live Guake tab, desktop notifications under a real systemd user manager, a real Agent running the `/baton-handoff` skill.

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

The Profiles inside a Project's sandbox are not listed: they come from the registered Projects that have `.ai-sandbox/run.sh`, and accept the tags of the Project's table (none by default):

```toml
[projects.my-app]                        # the Project's name, as `baton ls` shows it
tags = ["work"]                          # tags accepted by both Agents in this Project's sandbox
```

A Project's Docker Compose dev container is configured per Project, with its Claude and Codex Profiles; `dir` is a path inside the container, `~` is the container user's HOME. Such a Project's default Runtime is `compose`. `baton new` needs the two Baton mounts in the service (`~/.baton/bin` at `/baton/bin` read-only, `~/.baton/projects/<project>` at `/baton/project` read-write); if they are missing it prints the snippet to put in your own untracked override file, which must be listed in `files` after the Project's files. Adding them needs one recreate of the container, which ends the Sessions running in it. When there are several Profiles of an Agent, `--profile my-app.<key>` chooses:

```toml
[projects.my-app.compose]
files   = ["docker-compose.yml"]         # absolute, ~/..., or relative to the Project's root
service = "app"

[projects.my-app.compose.profiles.claude]   # the Profile is called "my-app.claude"
agent = "claude"
dir   = "~/.claude"                      # inside the container; may be a link into a volume
tags  = ["work"]

[projects.my-app.compose.profiles.codex]   # the Profile is called "my-app.codex"
agent = "codex"                          # dir may be left out: the service's CODEX_HOME, else ~/.codex
tags  = ["work"]

[profiles.shared-claude]                 # a volume shared by several Compose setups: configure it once
runtime = "compose"
project = "my-app"                       # the Project whose service hosts it
agent   = "claude"
dir     = "/state/.claude"
```

`baton sync` needs only `sh` and `tar` in the image (and the usual `mktemp`, `readlink`, `mv`); `baton new` also needs `env`. Baton never edits the Compose files and never recreates the service; `baton new` runs `docker compose up -d <service>` only when the service is not running.

Telegram notifications while you are away (`baton away` / `baton back`) need a table in the same file and the bot token in `secrets.env`:

```toml
[telegram]
token_var = "TELEGRAM_TOKEN"             # the NAME of the variable in secrets.env that holds the bot token
chat_id   = 123456789                    # a number, or a string like "@my_channel"
# api_url = "https://api.telegram.org"   # the default; http is accepted for localhost only
```

`baton watch` can play a sound itself after each desktop notification (not after a Telegram message), with `pw-play`, or else `paplay`, from your `PATH`. It does nothing unless you set `sound`: a name from the freedesktop sound theme (looked up as `<dir>/freedesktop/stereo/<name>.oga`, `.ogg` or `.wav` in `~/.local/share/sounds`, `/usr/local/share/sounds` and `/usr/share/sounds`), or the path of a file (`/...` or `~/...`). A sound that cannot be found stops `baton watch` with an error. The desktop's own event-sounds setting is not consulted. Restart the watcher after changing it.

```toml
[watch]
sound = "message-new-instant"           # or "~/sounds/ping.wav"
```

The source repository holds `mcp.json` (Claude's `mcpServers` shape; a server may carry `"tags": ["work"]`), `skills/<name>/SKILL.md` (plus any supporting files) and `AGENTS.md` with instructions. A skill is tagged with `tags: [work]` in its front matter, a part of `AGENTS.md` by wrapping it in `<!-- baton:tags work -->` and `<!-- /baton:tags -->` lines. Untagged items go to every Profile; a tagged one only to Profiles that accept at least one of its tags. Baton also installs its own skill, `baton-handoff`, into every Profile whatever the tags; skill names starting with `baton-` are reserved, and a source skill with such a name makes `baton sync` stop with an error. Secrets never go into the source: write `${VAR}` in `mcp.json` and put the values in `~/.config/baton/secrets.env` (`VAR=value` lines, mode 0600; `baton sync` refuses to run if others can read it). A Profile receives only the secrets of the servers it accepts. Baton records what it wrote in `~/.baton/manifests/<profile>.json` and only ever updates or deletes those files, blocks and keys. See [DESIGN.md](DESIGN.md#config-source-and-manifest-formats) for the formats.

## Documentation

- [DESIGN.md](DESIGN.md): goals, architecture and key decisions.
- [GLOSSARY.md](GLOSSARY.md): the domain vocabulary (Agent, Profile, Runtime, Session, Task, …).
- [docs/adr/](docs/adr/): decisions that are hard to reverse.
- [.scratch/baton-v1/](.scratch/baton-v1/): the v1 spec and its tickets.
- [AGENTS.md](AGENTS.md): conventions for coding agents working on this repository.

## License

[MIT](LICENSE)
