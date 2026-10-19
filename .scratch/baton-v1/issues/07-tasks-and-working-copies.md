# 07: Tasks and Working copies

**What to build:** every Session belongs to a Task, and the Working copy decides which one. `baton new` without a Task name creates an unnamed Task; I can name it later and mark it `done` or `dropped`. Baton refuses a second active Session in the same Working copy, and Sessions I start by hand land in the Working copy's current Task.

**Blocked by:** 06

**Status:** ready-for-agent

- [ ] `baton new <project> [task]` records the Task as current for the Working copy; without a name it creates an unnamed Task such as `<project>/<date>-<n>`.
- [ ] A Session in a Working copy without a current Task creates an unnamed Task and makes it current.
- [ ] A Session started by hand in a Working copy belongs to its current Task in `baton ls`.
- [ ] A new Session after `/clear` belongs to the same Task.
- [ ] `baton new` refuses when the Working copy already has an active Session, and suggests `baton handoff`.
- [ ] The current Task of a Working copy cannot change while a Session is active there.
- [ ] A Task can be named, and its status set to `open`, `done` or `dropped`; `baton ls` shows Task names.
