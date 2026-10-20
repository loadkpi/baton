# Baton

Baton is a personal orchestrator on top of the official [Claude Code](https://code.claude.com) and [Codex](https://github.com/openai/codex) CLIs. It is one Go binary with no server:

- **`baton sync`** renders one private repository of instructions, skills, MCP servers and status hooks into every Agent Profile: on the host, inside [`ai-sandbox`](https://github.com/loadkpi/ai-sandbox) containers and inside a project's Docker Compose dev container.
- **`baton new` / `baton ls`** start Sessions of either Agent in a named Guake tab and list every Session with a live status (`running`, `waiting_approval`, `waiting_input`, `ended`, `lost`), including Sessions started by hand.
- **`baton watch`** sends a desktop notification when an Agent needs you, or a one-way Telegram message while you are away.
- **`baton handoff`** moves a Task from one Agent or Profile to another without copying context by hand.

Baton never replaces the Agents and never touches their tokens: it runs the vendor CLIs under your own login and learns everything from their hooks.

## Status

Work in progress; v1 is being built ticket by ticket. Today part of `baton sync`, `baton ls`, `baton new` (host and sandbox) and Tasks work. `baton sync` handles skills, the instructions block in `CLAUDE.md`, MCP servers (with secrets from a local file) and Baton's status hooks in `settings.json` for host Claude Profiles, filtered by tags (work content goes only to the Profiles that accept it), with an owned-files manifest, a diff-and-confirm step and backups before Baton changes anything it did not create. MCP servers go to the Profile's `.claude.json` (`~/.claude.json` for the default `~/.claude` Profile, `<dir>/.claude.json` for the others). Sync refuses to put secrets into a file others can read. The status hooks call `~/.baton/bin/baton`, which `make install` puts there. After that, a Claude started by hand under any synced Profile (Sessions that were already running keep their old hooks) shows up in `baton ls` with its Project, Task, Agent, Profile, Runtime and status (`running`, `waiting_approval`, `waiting_input` or `ended`); Projects register themselves from the main checkout of the git repository of the Session's directory, and a git worktree is a Working copy of the same Project. This part is tested with payloads copied from the documentation and with stubs, not yet under a live Claude Code. `baton new <project> [task] --agent claude [--profile <name>]` (host Projects; sandbox below) makes a Task current in the Working copy and opens a Guake tab there (the Project's main checkout for a registered name, or the git repository or worktree whose path you give, which registers the Project), titled `<project>/<task>:claude`, running Claude Code under the chosen Profile with `ANTHROPIC_API_KEY` and `OPENAI_API_KEY` removed; the Profile defaults to `default_profile` or to the only Claude Profile. Without a task name it creates an unnamed Task (`2026-10-20-1`); a name continues that Task or creates it. `baton task name <project>/<task> <name>` names a Task later and `baton task status <project>/<task> <open|done|dropped>` closes it. A Session belongs to the Task that was current in its Working copy at its first Event, including a Session started by hand, and `baton new` refuses while a Session is active in the Working copy; a Session whose Agent died without ending shows as `lost` and no longer blocks it. `baton sync` also fills the Profiles inside the `ai-sandbox` of every registered Project that has one (`<project>.claude` and `<project>.codex`, in `.ai-sandbox/home`): Claude there gets what a host Profile gets, Codex gets skills and status hooks directly, and instructions and MCP servers as fragment files (`AGENTS.d/baton.md`, `config.d/baton.toml`) because `ai-sandbox` regenerates its Codex config and instructions on every start. Sandbox Profiles accept only untagged items unless the Project's table in the config says otherwise. For a Project that has `.ai-sandbox/run.sh`, `baton new <project> [task] --agent claude` opens the Guake tab through `ai-sandbox` instead: Baton runs the Project's own `run.sh` in its print-args mode, builds the image with `build.sh` when it is missing, merges the Codex fragments between `# baton:begin` / `# baton:end` (and `<!-- baton:begin -->` / `<!-- baton:end -->`) lines of the files `run.sh` rendered, mounts `~/.baton/bin` read-only and the Project's `~/.baton/projects/<project>` read-write (nothing else of `~/.baton`), and starts Claude with the two API key variables removed. It needs `make install` first (the container's hooks call `/baton/bin/baton`), works only in the Project's main checkout, and takes no `--profile` (the sandbox has its own Profile, `<project>.claude`). The hooks inside the container write their Events into the mounted directory, and `baton ls` shows the Session with Runtime `sandbox`, mapping its directory to the host through the mounts it started the container with; without the mount the hook does nothing. This part is tested against the `docker` stub and a copy of `run.sh`, never under a real Docker, Claude Code in a container or Codex. `baton new <project> [task] --agent codex` does the same for Codex in a sandbox Project (Codex runs only there; a host Project and a Codex Profile in the config are refused), under the Profile `<project>.codex`, and `baton ls` shows its status from the same five (`Stop` and `Interrupt` give `waiting_input`, `PermissionRequest` `waiting_approval`). Codex runs hooks only after you trust them with `/hooks`, which Baton never does for you: a Codex that `baton new` started and that has reported no Event is listed with the status `no_events` and a line telling you to run `/hooks`. This is tested with payloads built from the Codex documentation (only the `SessionEnd` example is verbatim) and a `codex` stub, never under a real Codex; whether real Codex sends exactly these fields and runs the written hook commands is not verified. The dev-container Runtime is not built. The Guake side is tested against a stub only, not yet against a live Guake. `baton ls` shows a Session as `lost` when its Agent process is gone without a `SessionEnd`: on the host when `/proc/<pid>` is missing or has another start time than the Event recorded (an Event from another machine is not judged), in a sandbox when its container is gone or, in a container that still runs, the recorded PID no longer runs there (one `docker ps` and one `docker exec` per running container for the whole listing; when docker does not answer, Sessions keep the status of their Events and one warning is printed). This is computed on every read and written nowhere. It is tested with dead and reused PIDs and a `docker` stub, never against a real container; a Session whose recorded PID is only the hook's parent (the Agent was not found among its ancestors) is never judged. `baton watch` runs until it is stopped and sends a desktop notification (`notify-send`) when a Session starts waiting for your approval, when a turn that took more than 30 seconds ends, and when a Session is lost: it looks at every Session every 2 seconds (`--interval`), tells only what changes after it started, and puts only the Task and the Agent in the message (`my-app/fix-login (claude)`), never a command or output. Sessions in a sandbox are told from the host; hooks never notify. `baton watch install` writes the `systemd --user` unit `~/.config/systemd/user/baton-watch.service` (it starts `~/.baton/bin/baton`, so run `make install` first) and runs `systemctl --user daemon-reload` and `systemctl --user enable --now baton-watch.service`. With the watcher stopped, `baton ls` and the Sessions work as before; what happens while it is down is not notified. This is tested against `notify-send` and `systemctl` stubs only, never on a real desktop or under a real systemd user manager. `baton away` makes the watcher send its notifications to Telegram instead of the desktop until `baton back`; the running watcher notices the change on its next look. The message is the same two lines as the desktop notification (status; Task and Agent) and is sent one-way with the Bot API `sendMessage` (nothing is read from Telegram). If Telegram fails, the message goes to the desktop and one warning per kind of failure is printed. `baton away` refuses unless the config has a `[telegram]` table and `secrets.env` has the token (see Configuration); the token stays on the host and never enters a sandbox. This is tested against a local fake HTTP server only: the real Telegram Bot API was never contacted; the request shape follows its official documentation. `baton handoff` and Compose are designed but not built yet.

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

Telegram notifications while you are away (`baton away` / `baton back`) need a table in the same file and the bot token in `secrets.env`:

```toml
[telegram]
token_var = "TELEGRAM_TOKEN"             # the NAME of the variable in secrets.env that holds the bot token
chat_id   = 123456789                    # a number, or a string like "@my_channel"
# api_url = "https://api.telegram.org"   # the default; http is accepted for localhost only
```

The source repository holds `mcp.json` (Claude's `mcpServers` shape; a server may carry `"tags": ["work"]`), `skills/<name>/SKILL.md` (plus any supporting files) and `AGENTS.md` with instructions. A skill is tagged with `tags: [work]` in its front matter, a part of `AGENTS.md` by wrapping it in `<!-- baton:tags work -->` and `<!-- /baton:tags -->` lines. Untagged items go to every Profile; a tagged one only to Profiles that accept at least one of its tags. Secrets never go into the source: write `${VAR}` in `mcp.json` and put the values in `~/.config/baton/secrets.env` (`VAR=value` lines, mode 0600; `baton sync` refuses to run if others can read it). A Profile receives only the secrets of the servers it accepts. Baton records what it wrote in `~/.baton/manifests/<profile>.json` and only ever updates or deletes those files, blocks and keys. See [DESIGN.md](DESIGN.md#config-source-and-manifest-formats) for the formats.

## Documentation

- [DESIGN.md](DESIGN.md): goals, architecture and key decisions.
- [GLOSSARY.md](GLOSSARY.md): the domain vocabulary (Agent, Profile, Runtime, Session, Task, …).
- [docs/adr/](docs/adr/): decisions that are hard to reverse.
- [.scratch/baton-v1/](.scratch/baton-v1/): the v1 spec and its tickets.
- [AGENTS.md](AGENTS.md): conventions for coding agents working on this repository.

## License

[MIT](LICENSE)
