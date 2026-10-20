# Baton

A personal orchestrator that launches the official Claude Code and Codex CLIs, tracks their sessions, and hands work over between them.

## Language

### Actors

**Agent**:
A vendor CLI that does the work: `claude` or `codex`.
_Avoid_: model, assistant, bot

**Subagent**:
A helper worker inside an Agent. Baton does not model it and only sees it through Events.
_Avoid_: agent (in this sense)

**Profile**:
An Agent's config directory with its own login, history and limits, e.g. `claude-personal`, `claude-work`, `codex` inside a specific project's sandbox, or `claude` inside a project's dev container.
_Avoid_: account, config, CLAUDE_CONFIG_DIR

**Daemon**:
Baton's background process on the host that watches Agent Events and tells the human about them. It is not an Agent.
_Avoid_: orchestrator agent, conductor, coordinator, orch

### Work

**Project**:
A git repository that Baton knows about. A Project has Working copies and Tasks.
_Avoid_: repo, workspace

**Runtime**:
Where an Agent runs: on the host (`host`), in the project's `ai-sandbox` container started for the Session (`sandbox`), or in a long-lived service of the project's own Docker Compose setup, such as a dev container with the application (`compose`).
_Avoid_: environment, Docker, container

**Working copy**:
A directory with the Project's files where an Agent works: the main checkout or a worktree. At most one Session is active in a Working copy at any time.
_Avoid_: worktree (as the general name), checkout, folder

**Task**:
A unit of work with a goal that outlives Sessions and Agents. Every Session belongs to a Task. A Task may stay unnamed until someone names it.
_Avoid_: ticket, feature, job

**Session**:
One native Agent conversation under one Profile, with exactly one native session id. `/clear` or switching conversations starts a new Session.
_Avoid_: run, chat, process

**Event**:
A record of something that happened in a Session, as reported by the Agent (the one exception is the `SessionEnd` that `baton session end` writes, which says it is Baton's own). Everything Baton knows about a Session, including its status, is derived from Events.
_Avoid_: log, message, trace

**Terminal**:
Where interactive work happens: a terminal tab, optionally attached to a multiplexer session. Several Sessions may run one after another in the same Terminal.
_Avoid_: session (in the tmux sense), window

**Interface**:
How a Session is driven: `tui` (the Agent's native UI in a Terminal) or `headless` (no human, structured output).
_Avoid_: mode

**Policy**:
A named set of rules for which Agent actions are allowed, denied, or need human approval, e.g. `default` or `night`.
_Avoid_: mode, permissions, night mode

**Handoff**:
The document a next Session uses to continue someone else's work. The automatic part is always present; the narrative part is written by the handing-over Session or, if it did not manage to, by the receiving one.
_Avoid_: context transfer, summary
