# Spec: Baton v1

Status: ready-for-agent

Source of truth for decisions: `DESIGN.md` (D1–D8), `GLOSSARY.md`, `docs/adr/0001-ai-sandbox-knows-nothing-about-baton.md`. This spec uses the glossary's terms; capitalized words (Agent, Profile, Project, Runtime, Working copy, Task, Session, Event, Handoff, Daemon, Terminal) carry exactly the glossary meaning.

## Problem Statement

I run Claude Code on the host under several Profiles (`~/.claude`, `~/.claude-personal`, `~/.claude-work`) and Codex only inside the per-project `ai-sandbox` container, where every Project has its own login and history. Some Projects also have their own long-lived Docker Compose dev container with the application, and I run Claude Code and Codex inside it too, with their Profiles in Docker volumes. Every Agent × Profile × Project is a separate world:

- I maintain instructions, skills, MCP servers and hooks by hand in several formats and places; skills already arrive from three different sources.
- I cannot see which Sessions are working, which wait for my approval, and which handed the turn back to me, so I keep switching Guake tabs to check.
- When an Agent needs me while I am looking elsewhere or away from the desk, I find out late.
- Moving a Task from Claude to Codex (or between two Claude Profiles) means copying context by hand.
- Guake tabs are not tied to Projects or Tasks.

## Solution

A single Go binary, `baton`, with no stateful Daemon. All state lives in files under `~/.baton` and `~/.config/baton`.

- `baton sync` renders one private source repository of instructions, skills, MCP servers and status hooks into every Profile, on the host, inside each Project's sandbox and inside Projects' compose dev containers, filtered by tags, with secrets substituted from a local file.
- The Agents' own hooks call `baton hook <agent>`, which only appends the raw Event to the Session's Event file. Session statuses are derived from Events.
- `baton new` starts a Session of either Agent in either Runtime in a named Guake tab, attached to a Task.
- `baton ls` lists all Sessions with live statuses, including Sessions I started by hand.
- `baton watch`, a stateless Daemon under `systemd --user`, sends desktop notifications, or one-way Telegram messages while I am away, including when a Session becomes `lost`.
- `baton handoff` plus a `/baton-handoff` skill let a new Session of any Agent or Profile continue a Task without manual copying.

## User Stories

### Sync

1. As a user, I want one private git repository to hold my instructions, skills, MCP servers and hook definitions, so that I edit them in one place.
2. As a user, I want to point Baton at that repository in my Baton config, so that the Baton tool itself contains none of my personal content.
3. As a user, I want `baton sync` to render instructions into every Profile in the format each Agent reads, so that Claude and Codex follow the same rules.
4. As a user, I want `baton sync` to install skills into every Profile, so that the same `SKILL.md` works in both Agents.
5. As a user, I want `baton sync` to render one MCP manifest into each Agent's own MCP configuration, so that I declare each server once.
6. As a user, I want `baton sync` to install Baton's status hooks into every Profile, so that every Session reports Events without manual setup.
7. As a user, I want to tag items in the source (`work`, `personal`, …) and give each Profile a list of tags it accepts, so that work skills and MCP servers never reach personal Profiles or personal Project sandboxes.
8. As a user, I want untagged instructions and skills to go to every Profile, so that common content needs no tagging.
9. As a user, I want the source to contain only `${VAR}` placeholders for secrets, with values in a local secrets file readable only by me, so that I can version the source safely.
10. As a user, I want a Profile to receive only the secrets of items its tags accept, so that a sandbox gets no more credentials than it needs.
11. As a user, I want `baton sync` to target host Profiles and the Profiles inside each sandbox Project's `.ai-sandbox/home`, so that Codex in the sandbox gets the same content as Claude on the host.
12. As a user, I want `baton sync` to never write files tracked by git, so that team repositories are never touched.
13. As a user, I want `baton sync` to remember every file it created and to update or delete only those, so that Claude's own `skills/synced/`, my existing work skills and third-party installs survive.
14. As a user, I want `baton sync`, before it first adds its part to a file or directory it does not own (`CLAUDE.md`, `settings.json`, the MCP configuration, a skill with the same name), to show me the diff, ask, and back it up, and to stop with a clear error when it cannot ask, so that it never silently changes something it does not own.
15. As a user, I want removing an item from the source to remove it from Profiles on the next sync, so that Profiles do not accumulate stale content.
16. As a user, I want `baton sync` inside the sandbox to write only content (instructions, skills, MCP, status hooks) and leave security settings (permissions, `sandbox_mode`, network) to `ai-sandbox`, so that the sandbox's security model stays intact.
17. As a user, I want sandbox Codex instructions and MCP servers to survive `run.sh` regenerating `config.toml` and `AGENTS.md` on every start, so that synced content is not silently lost.

### Projects, Working copies and Tasks

18. As a user, I want a Project to be registered automatically the first time I run `baton new` in a git repository or Baton sees a Session there, so that there is no setup step.
19. As a user, I want a Project's default Runtime to be `sandbox` when it has `.ai-sandbox/run.sh` and `host` otherwise, so that Baton matches how I already work.
20. As a user, I want `baton new <project>` without a Task name to create an unnamed Task, so that a quick question costs no ceremony.
21. As a user, I want to name an unnamed Task later, so that it becomes a real Task once the work turns out to matter.
22. As a user, I want to mark a Task `done` or `dropped`, so that its state reflects my decision rather than whether a Session happens to be running.
23. As a user, I want the Working copy to determine the Task, so that every Session there, including one I started by hand with `claude-personal`, lands in the right Task.
24. As a user, I want a Session in a Working copy with no current Task to create an unnamed Task and make it current, so that no Session is ever orphaned.
25. As a user, I want Baton to refuse a second active Session in the same Working copy, so that two Agents never edit the same files at once.
26. As a user, I want Baton to refuse changing a Working copy's current Task while a Session is active there, so that Events always belong to one Task.
27. As a user, I want `/clear` or switching conversations inside a Terminal to start a new Session of the same Task, so that one tab maps to one Task.

### Sessions and statuses

28. As a user, I want `baton new <project> [task] --agent <claude|codex>` to open a new Guake tab titled `<project>/<task>:<agent>` running the Agent in the Project's Runtime, so that tabs are tied to Tasks.
29. As a user, I want to choose the Profile for a host Session, so that I can work under `claude-personal` or `claude-work`.
30. As a user, I want Baton to remove `ANTHROPIC_API_KEY` and the OpenAI key from the environment when it launches an Agent on the host, so that I am never switched to API billing by accident.
31. As a user, I want sandbox Sessions to run through `ai-sandbox` with only this Project's Baton directory mounted read-write and the Baton binary mounted read-only, so that a sandbox never sees other Projects.
32. As a user, I want `baton ls` to show every Session across Projects and Profiles with its Project, Task, Agent, the start of its session id, Profile, Runtime and status, so that I see everything in one place.
33. As a user, I want Session status to be one of `running`, `waiting_approval`, `waiting_input`, `ended`, `lost`, so that the list is simple and means the same for both Agents.
34. As a user, I want a Session to show `waiting_input` after the Agent finishes a turn, so that I know it is my turn even though Codex has no "waiting for input" hook.
35. As a user, I want a Session whose process or container died without `SessionEnd` to show `lost`, so that crashed Sessions do not look alive.
36. As a user, I want `baton ls` to flag a Codex Session that has reported no Events and point me to `/hooks`, so that I know to trust the hooks in a new sandbox.
37. As a user, I want Sessions I started by hand outside Baton to appear in `baton ls`, so that nothing is invisible.
38. As a user, I want every Event to be stored raw, one line per Event, in a per-Session file readable only by me, so that I have an archive I can search with `rg`.
39. As a user, I want the hook to do nothing but append the Event, so that it never slows an Agent down, including within Codex's 1-second `SessionEnd` budget.
40. As a user, I want Baton's hooks inside a sandbox without the Baton mount to silently do nothing, so that `ai-sandbox` keeps working without Baton.

### Watcher and notifications

41. As a user, I want `baton watch` to run as a `systemd --user` service, so that notifications work without me starting anything.
42. As a user, I want a desktop notification whenever a Session enters `waiting_approval`, so that an Agent never waits on me unnoticed.
43. As a user, I want a desktop notification when a Session enters `waiting_input` only if the turn took longer than 30 seconds, so that I am not spammed during quick back-and-forth.
44. As a user, I want a notification when a Session becomes `lost`, so that I learn about crashes.
45. As a user, I want `baton away` to route notifications to Telegram until `baton back`, so that I learn about waiting Agents when I am away from the desk.
46. As a user, I want Telegram messages to contain only the status and the Task name, never command output, so that nothing sensitive leaves the machine.
47. As a user, I want notifications for sandbox Sessions to be sent from the host, so that the Telegram token and my desktop session never enter a container.
48. As a user, I want Sessions and `baton ls` to keep working when `baton watch` is down, so that only notifications are lost.

### Handoff

49. As a user, I want `baton handoff <task> --to <agent>[@<profile>]` to write the automatic part of a Handoff and open a new Session whose first message points to it, so that a Task moves between Agents or Profiles with one command.
50. As a user, I want `baton handoff` to wait, saying so, until the Task's still-active Session ends (I type `/exit` there, or its process dies) before opening the new one, so that the one-active-Session rule holds during a Handoff.
51. As a user, I want the automatic part to contain the Task, Working copy, branch, HEAD, `git status`, `git diff --stat`, the last 10 commands with exit codes from Events, and a link to the Session's Event file, so that the receiver sees the factual state.
52. As a user, I want a `/baton-handoff` skill in both Agents that writes the narrative part (goal; what is done; decisions and why; rejected options and why; one concrete next step; open questions; how to verify) and saves it through `baton handoff save`, so that the reasoning survives the switch.
53. As a user, I want `baton handoff save` to find the Task from the current directory, so that the skill works the same on the host and in the sandbox without environment variables.
54. As a user, I want the receiving Session to write the narrative part on its first turn when the handing-over Session could not (for example, its limit ran out), so that a Handoff is never only facts.
55. As a user, I want the Handoff header to say who wrote the narrative part, so that I know how much to trust it.
56. As a user, I want Handoffs stored under `~/.baton`, never in the Working copy, so that repositories stay clean.
57. As a user, I want to run `/baton-handoff` before or after starting `baton handoff`, so that I do not have to remember an order.

### Compose Runtime

58. As a user, I want to declare that a Project's Agents run in a service of the Project's own Docker Compose setup (compose files and service name in my Baton config), so that Baton treats that dev container as a third Runtime, `compose`.
59. As a user, I want `baton sync` to render content into the Profiles inside that container, including Profiles that live in named Docker volumes shared by several Compose setups, so that the dev container gets the same instructions, skills, MCP servers and status hooks as the host.
60. As a user, I want Baton to never edit the Project's Compose files, and to tell me exactly which mounts to add to my untracked override file when the container lacks Baton's mounts, so that team files stay untouched.
61. As a user, I want `baton new` to open a Guake tab that starts the service if needed and runs the Agent in it with `docker compose exec` in the Project's directory inside the container, so that Sessions in the dev container are tied to Tasks like any other.
62. As a user, I want paths inside the container mapped back to host paths from the container's mounts, so that Sessions in the dev container land in the right Project, Working copy and Task, and count for the one-active-Session rule together with host Sessions in the same checkout.
63. As a user, I want a Session whose Agent process died inside a container that is still running to show `lost`, so that a long-lived dev container does not hide crashed Sessions.

### Recovery

64. As a user, I want to end a Session by hand with `baton session end <session>` when Baton cannot tell that it is dead, so that a crashed Session never blocks its Working copy or a Handoff forever.

## Implementation Decisions

**Shape**

- One statically linked Go binary (`CGO_ENABLED=0`) provides every command: `sync`, `new`, `ls`, `watch`, `hook`, `handoff`, `handoff save`, `away`, `back`. It lives in `~/.baton/bin` so the same binary can be mounted read-only into sandboxes (the sandbox image has `python3`, `bash`, `jq`, no Go).
- No stateful Daemon and no API in v1. Truth lives in files; `baton watch` is a stateless Daemon that reads them.
- `baton hook <agent> --profile <name> --runtime <host|sandbox|compose>` (flags written by `baton sync` into each Profile's hooks) reads the hook payload from stdin and appends one line to the Session's Event file under a file lock (Claude runs hooks in parallel and `PostToolUse` payloads exceed the atomic append size). The line holds `v`, `ts`, `agent`, `profile`, `runtime`, `host` (container id in a sandbox), `agent_pid` and `agent_start` (process start time, against PID reuse), `session_id`, `event`, `cwd` and the raw `payload`. No git calls, no Project lookup, no payload interpretation.
- All mapping from Events to statuses, notification rules and `lost` detection lives in one place in Go, shared by `baton ls` and `baton watch`.

**State layout (a contract the tests rely on)**

- `~/.config/baton/`: config (source repository path, Profiles and their accepted tags) and a secrets file with mode 0600.
- `~/.baton/bin/` (the binary), a sync manifest per Profile (owned files, and owned blocks/keys in shared files), `~/.baton/backups/<profile>/<ts>/` with copies of shared files taken before sync first changed them.
- `~/.baton/sessions/<agent>-<session>.jsonl`: raw Events of host Sessions (mode 0600), keyed by Session only.
- `~/.baton/projects/<project>/`: Project record (path, default Runtime), Task records (title, status), `current.jsonl` (journal of the current Task per Working copy, with timestamps), `sessions/<agent>-<session>.jsonl` with raw Events of sandbox Sessions (mode 0600), `handoffs/<task>/<n>.md` and `handoffs/<task>/draft.md`. Only this directory of the Project is mounted into its sandbox.

**Domain rules**

- Project → Working copy → Task → Session → Event; a Session also has an Agent, a Profile and a Runtime.
- A Session is one native conversation (one native session id).
- At most one active Session per Working copy.
- The current Task of a Working copy is appended to `current.jsonl` by `baton new`; a Session belongs to the Task that was current in its Working copy at its first Event; no record → create an unnamed Task and make it current; the current Task changes only when no Session is active there.
- Readers (`ls`, `watch`, `new`, `handoff`), not the hook, resolve Project and Working copy from the Session's `cwd` (`git rev-parse --show-toplevel`) and register an unknown Project by its git root under a lock; a name clash gets a suffix. Sessions outside git show Project `-`.
- Task status is stored as `open` / `done` / `dropped`; activity is derived from Sessions.
- v1 sandbox and compose Sessions run only in the Project's main checkout (worktree paths do not resolve inside a container, and `.ai-sandbox/` is gitignored).

**Runtimes**

- A Runtime is a replaceable part of the code with one interface: how to start an Agent in it, where its Profiles are and how to write into them, how to map a path inside it to a host path, and whether a Session's Agent process is still alive. `host`, `sandbox` and `compose` implement it; ticket 08 introduces the interface and later Runtimes plug in without changing the callers.
- Paths from Events (`cwd`) of container Runtimes are mapped to host paths through the container's mounts (`docker inspect`), never by assuming the paths are equal.
- Liveness in container Runtimes: the container is gone, or it runs but the recorded `agent_pid` (in the container's PID namespace) is gone or has another start time, checked with one `docker exec` per running container per check.
- `compose`: configured per Project in the Baton config (Compose files, service, the Agents' Profiles inside it with their tags). The Project's Compose files know nothing about Baton, the same principle as ADR 0001: Baton never edits them, checks its two mounts (`~/.baton/projects/<project>` read-write, `~/.baton/bin` read-only) with `docker inspect`, and when they are missing prints the snippet for the user's untracked override file and refuses to start. Profiles inside the container are full Profiles (no fragment treatment); Baton writes into them through Docker, since named volumes are not writable from the host. A Profile in a volume shared by several Compose setups is one Profile, synced once.

**Status derivation** (hook names checked against both vendors' docs on Oct 20, 2026)

| Status | Claude Code Events | Codex Events |
| --- | --- | --- |
| `running` | `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure` | `UserPromptSubmit`, `PreToolUse`, `PostToolUse` |
| `waiting_approval` | `PermissionRequest` | `PermissionRequest` |
| `waiting_input` | `SessionStart`, `Stop` | `SessionStart`, `Stop`, `Interrupt` |
| `ended` | `SessionEnd` | `SessionEnd` |
| `lost` | process gone (inside its container, for container Runtimes) or container gone, without `SessionEnd` | same |

**Notifications**

- `waiting_approval` and `lost`: always. `waiting_input`: only when the turn (from `UserPromptSubmit` to `Stop`) took longer than 30 seconds.
- Channel: desktop via `notify-send` by default; Telegram, one-way, between `baton away` and `baton back`. Messages carry status and Task name only.
- Sent only by `baton watch` on the host, never by hooks.
- `baton watch` keeps no state on disk in v1: at start it sends nothing, then notifies only transitions it sees; `lost` by polling the recorded PIDs and one `docker ps` per tick. Notifications while it is down are lost. A cursor file that survives restarts is v2.

**Sync**

- Source is a separate private git repository. Targets are Profiles only: host Profiles from config, plus Claude and Codex Profiles inside each sandbox Project's `.ai-sandbox/home`.
- Mapping per Agent as in `DESIGN.md` D4: instructions (`AGENTS.md` → `CLAUDE.md` / `AGENTS.md`), skills (same `SKILL.md`, different directories), one MCP manifest, generated status hooks (`settings.json` for Claude, `hooks.json` for Codex).
- Tags filter items per Profile; `${VAR}` placeholders resolved from the secrets file; a Profile receives only the secrets of items it accepts.
- Owned-files manifest; update/delete only owned files; never write git-tracked files.
- Baton's built-in skills are named `baton-*` (`/baton-handoff`); the prefix is reserved, and a source skill named `baton-*` is an error.
- Shared files (`CLAUDE.md`, `settings.json`, the Claude MCP configuration, Codex `hooks.json`): Baton owns only a marked block (Markdown) or named keys (JSON/TOML). First change to such a file: show the diff, ask, back up to `~/.baton/backups/`, write atomically. Later changes to Baton's own part need no question unless it was edited by hand. A foreign skill directory in the way gets the same question and backup. No terminal to ask in → stop with an error naming the path.
- In the sandbox, Baton writes only content fragments; `ai-sandbox` owns `config.toml`/`AGENTS.md` generation and security settings.

**Sandbox integration** (ADR 0001)

- `ai-sandbox` never mentions Baton. Baton uses a generic `ai-sandbox` mechanism when one makes sense without Baton (extra mounts, config fragments directories); otherwise it wraps `run.sh` via `AI_SANDBOX_PRINT_ARGS` (configs are rendered, then the docker invocation is printed one argument per line), merges its block between its own markers, appends its mounts, and runs `docker` itself, building the image via `build.sh` if missing.
- Mounts: `~/.baton/projects/<project>` read-write, `~/.baton/bin` read-only.
- Codex hook trust (`/hooks`) is never bypassed.

**Handoff**

- Stored under `~/.baton/projects/<project>/handoffs/<task>/<n>.md`.
- Automatic part by `baton handoff`; narrative part by the `/baton-handoff` skill through `baton handoff save` (stdin). The skill ships with Baton as a built-in item that `baton sync` installs into every Profile regardless of tags; it is not part of the user's source repository, Task resolved from the current directory; receiver fills the narrative part if missing; header records the author.
- If the Task's Session is still active, `baton handoff` prints that it is waiting and blocks until that Session gets `SessionEnd` or becomes `lost`. Only then it takes the automatic part (fresh git state and commands), joins it with `draft.md` if present into `<n>.md`, removes the draft and opens the receiving Session. This keeps the one-active-Session-per-Working-copy rule and makes the order of `/baton-handoff` and `baton handoff` irrelevant.
- `baton handoff save` fills the Task's latest `<n>.md` if it has no narrative part (author: receiving), otherwise writes `draft.md` for the next Handoff (author: handing-over). Header: `narrative: handing-over | receiving | missing`. Edge accepted for v1: an unfilled Handoff receives the next `/baton-handoff`'s text.
- A Handoff to a sandbox Agent from a Working copy other than the main checkout is refused.
- The receiving Session starts with "continue the Task from Handoff `<path>`" as its first message, plus a request to write the narrative part when it is missing.

**Terminal**

- `baton new` opens a Guake tab with one call, `guake -e '<line>' -r '<title>'`, where the line is `cd <project path> && <command>` (`-e` opens a new tab in Guake's default directory and types the line into it, `-r` titles the current tab, and `-n` is ignored when `-e` is given; Guake 3.10 source: `main.py`, `dbusiface.py` `execute_command`, `notebook.py` `new_page_with_focus`; see `DESIGN.md` D8). The tab runs the Agent directly. Static title. No tab switching from notifications. The Terminal is a replaceable part so tmux can be plugged in after the experiment.

## Testing Decisions

- **One seam: the `baton` executable run as a subprocess.** Every test builds the binary, creates an isolated `HOME` (with `~/.baton`, `~/.config/baton` and Profile directories) and a temporary git repository as the Project, runs `baton <command>`, and asserts on stdout, exit code and files. No tests against internal Go packages; the Event → status table is covered through the CLI with fixtures, which keeps internals free to refactor.
- **External world is stubbed on `PATH`:** `guake`, `docker`, `notify-send`, `claude`, `codex` are stub executables that record their arguments. Telegram is a local HTTP server whose URL is set in the Baton config. Git is real.
- **A good test checks external behavior only:** files in Profiles, Event files, `baton ls` output, stub invocations, HTTP requests received. Never internal function calls.
- **Per area:**
  - Sync: a fixture source repository with tags and `${VAR}`s → `baton sync` → Profile contents and manifest; foreign shared file → diff and question (answered through a pseudo-terminal), backup, merged block; no terminal → stop with error; git-tracked files untouched; removing a source item removes it from Profiles.
  - Statuses: recorded real hook payloads from Claude Code and Codex (fixtures) piped into `baton hook <agent>` → `baton ls`. These fixtures are the guard against CLI changes; refresh them when bumping CLI versions.
  - Watcher: start `baton watch` as a process, then write Event files with chosen timestamps (Events present before the start produce nothing), assert `notify-send` stub calls and fake-Telegram requests. The 30-second rule and `lost` (a dead PID in the Events) are driven by data in Events, so no clock control or test-only flags are needed.
  - Sandbox: the fixture Project contains a real copy of `.ai-sandbox/run.sh`; the `docker` stub records arguments; assert mounts and fragments between markers. This exercises the `AI_SANDBOX_PRINT_ARGS` contract from ADR 0001.
  - Compose: a fixture Project with a Compose file; the `docker` stub answers `docker inspect` (mounts, state) and `docker exec` (PID checks) from fixtures and records every call; assert synced Profile contents as written through the stub, the printed override snippet when mounts are missing, the `docker compose exec` arguments of `baton new`, path mapping in `baton ls`, and `lost` for a dead PID in a running container.
  - Handoff: Markdown on stdin to `baton handoff save` before, and in a second test after, `baton handoff --to codex` started waiting → Handoff file contents and the `codex` stub's arguments. With an active Session, the `codex` stub is not invoked until the test appends a `SessionEnd` Event (or the recorded PID is dead).
- **Prior art:** none in `baton` yet. `ai-sandbox`'s `test/run-args.test.sh` tests `run.sh` the same way, through print-args mode without Docker.

## Out of Scope

- A stateful Daemon with an API; the web dashboard and its embedded terminal.
- Two-way Telegram: approvals, new Tasks, commands.
- A unified permission policy (`policy.yaml`) and a shared `PreToolUse` approval hook; inside the sandbox, `ai-sandbox` modes act as the Policy.
- Slash commands and subagents in `baton sync`.
- Headless Sessions, the `failed` status, queue, scheduler, night runs and budgets.
- Per-turn checkpoints in git refs and `baton undo`.
- Worktrees inside the sandbox; parallel Tasks in one sandbox Project.
- Shared Profiles across sandbox Projects (to be discussed for v2).
- Limit snapshots and automatic Agent/Profile switching on limit.
- Normalizing or parsing the Agents' native session files; search beyond `rg`; secret masking and retention for Events.
- Collecting failed tests in Handoffs.
- Live status in Guake tab titles; jumping to a tab from a notification.
- tmux as a dependency; evaluating Agent Deck and similar tools.
- A decision log as a Baton feature (ADRs, glossary and a skill delivered by sync cover it).
- Other Agents (Gemini CLI, OpenCode), several machines, quality gates, cost per Task, evals, tracker integrations.
- Resuming a native Session from Baton's archive; replacing the Agents' TUI; exact limit accounting.

## Further Notes

- Build order follows `DESIGN.md`'s v1 plan, each stage usable on its own: (1) `baton sync`, (2) Sessions and statuses, (3) watcher and notifications, (4) Handoff, (5) the `compose` Runtime (tickets 16–18, Codex in it: 19), built on the Runtime interface from tickets 08–11; (6) ending a stuck Session by hand (20).
- Unverified facts for the `compose` Runtime: how to write into a Profile in a named volume (`docker cp` into a running container, `docker compose exec` with a tar stream, or a short-lived `docker run` with the volume; ownership and UID inside the container); whether an Agent inside a container sees the `ANTHROPIC_API_KEY`/OpenAI key of the container's environment and how `docker compose exec` can unset them; whether the hook's `agent_pid` inside a container is the Agent or a shell.
- Unverified facts to check at the start of the relevant stage: whether interactive `codex` accepts an initial prompt as a positional argument (Handoff); what the `NEW_TAB` argument of `guake -n` means (Terminal); whether the hook's parent process is the Agent or a shell (Sessions); whether Codex payloads use Claude's field names (Sessions); whether `PostToolUse` payloads carry exit codes (Handoff); whether Claude rewrites `.claude.json` while running (Sync); whether `run.sh` regenerates Codex `hooks.json` or skills (Sync).
- Not decided in the review, left to implementation: exact command names for naming a Task and setting its status; which tags sandbox Profiles accept by default; how items carry tags in the source and the MCP manifest's file name and format; the command that installs the `systemd --user` unit. Each is chosen in its ticket and recorded in `DESIGN.md`.
- tmux is being tried for a week inside Guake; its result decides the default Terminal for v2, not v1.
