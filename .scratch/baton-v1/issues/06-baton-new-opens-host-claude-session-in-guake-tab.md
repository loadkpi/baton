# 06: `baton new` opens a host Claude Session in a Guake tab

**What to build:** `baton new <project> --agent claude` opens a new Guake tab titled `<project>/<task>:<agent>` that runs Claude Code under the Profile I choose, in the Project's directory, and the Session shows up in `baton ls`.

**Blocked by:** 05

**Status:** done (live Guake check pending)

- [x] Before building: check what the `NEW_TAB` argument of `guake -n` means and record it in `DESIGN.md` D8.
- [x] `baton new` registers the Project if it is not known yet.
- [x] `baton new` invokes Guake to open a new tab in the Project's Working copy with the expected title and command (verified through a `guake` stub).
  - Guake itself was not driven: the stub test shows the calls Baton makes, not that a live Guake 3.10 accepts them (see `DESIGN.md` D8, `[unverified]`). `-n` is ignored when `-e` is given, and `-e` itself opens the tab, so it is one call, `guake -e 'cd <dir> && …' -r <title>`.
- [x] The Profile can be chosen; the default is configurable.
- [x] `ANTHROPIC_API_KEY` and the OpenAI key are removed from the environment of the launched Agent.
- [x] The Terminal is a replaceable part: the code that opens the tab is separate from the code that decides what to run.
