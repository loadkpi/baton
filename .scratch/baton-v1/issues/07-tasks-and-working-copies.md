# 07: Tasks and Working copies

**What to build:** every Session belongs to a Task, and the Working copy decides which one. `baton new` without a Task name creates an unnamed Task; I can name it later and mark it `done` or `dropped`. Baton refuses a second active Session in the same Working copy, and Sessions I start by hand land in the Working copy's current Task.

**Blocked by:** 06

**Status:** done

- [x] `baton new <project> [task]` appends the Task to the Project's `current.jsonl` as current for the Working copy, with a timestamp; without a name it creates an unnamed Task such as `<project>/<date>-<n>`.
- [x] A Session belongs to the Task that was current in its Working copy at its first Event, no matter when it is first read (test: a Session ends, `baton new` switches the Task, only then the first `baton ls`).
- [x] A Session in a Working copy without a current Task creates an unnamed Task and makes it current.
- [x] A Session started by hand in a Working copy belongs to its current Task in `baton ls`.
- [x] A new Session after `/clear` belongs to the same Task.
- [x] `baton new` refuses when the Working copy already has an active Session, and suggests `baton handoff`.
  - The message names the blocking Session and suggests `baton handoff`, which does not exist until ticket 14. A Session that crashed without `SessionEnd` blocks its Working copy until ticket 11 (`DESIGN.md` D1).
- [x] The current Task of a Working copy cannot change while a Session is active there.
- [x] A Task can be named, and its status set to `open`, `done` or `dropped`; `baton ls` shows Task names. The command names are chosen here and recorded in `DESIGN.md` D1.
