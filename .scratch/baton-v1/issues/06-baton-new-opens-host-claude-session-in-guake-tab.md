# 06: `baton new` opens a host Claude Session in a Guake tab

**What to build:** `baton new <project> --agent claude` opens a new Guake tab titled `<project>/<task>:<agent>` that runs Claude Code under the Profile I choose, in the Project's directory, and the Session shows up in `baton ls`.

**Blocked by:** 05

**Status:** ready-for-agent

- [ ] `baton new` registers the Project if it is not known yet.
- [ ] `baton new` invokes Guake to open a new tab in the Project's Working copy with the expected title and command (verified through a `guake` stub).
- [ ] The Profile can be chosen; the default is configurable.
- [ ] `ANTHROPIC_API_KEY` and the OpenAI key are removed from the environment of the launched Agent.
- [ ] The Terminal is a replaceable part: the code that opens the tab is separate from the code that decides what to run.
