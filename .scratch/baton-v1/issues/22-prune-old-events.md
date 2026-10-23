# 22: Prune old Event files

**What to build:** `baton prune --older-than <N>d [--dry-run]` deletes the Event files of Sessions that ended or were lost more than N days ago, so the archive (D7) does not grow without limit and `baton watch`, which reads the first and newest Event of every file on every look, does not slow down. Nothing is deleted unless I run it.

**Blocked by:** 21

**Status:** done

- [x] `--older-than` is required, a whole number of days of 1 or more (`90d`; a bare `90` is also accepted); anything else is a usage error and nothing is deleted. There is no default and no automatic run: Baton keeps no timer and `baton watch` never prunes.
- [x] A Session is pruned when its status is `ended` or `lost` and the time of its newest Event (the Agent's or Baton's) is more than N days before now. A Session that is `running`, `waiting_approval`, `waiting_input` or `unknown`, and any Session newer than the limit, is never touched. Status is judged exactly as `baton ls` judges it (`sessionbus.List`), host, sandbox and compose Sessions alike (their files live under `~/.baton/projects/<project>/sessions/`).
- [x] What is deleted is the Session's Event file and nothing else: not Handoffs, not Tasks, not `project.toml`, not the `current.jsonl` journal, not the Project's directory. A Handoff keeps working; its link to the Event file may now point at nothing, and `DESIGN.md` says so.
- [x] The output lists each file it deleted (or, with `--dry-run`, would delete; `--dry-run` deletes nothing) with the Session as `baton ls` names it, then a one-line total with the number of files and bytes. With nothing to prune it says so and exits 0. A file that cannot be deleted is reported on stderr, the rest are still processed, and the exit code is 1.
- [x] It takes the same lock as `baton hook` for the file it deletes (a hook appending at that moment either lands before the delete or creates a new file for the same session id; a Session never loses a line silently, and a Session that comes back after its file was pruned simply starts a new file). `[unverified]` how an Agent behaves when it resumes a pruned session id; the test only shows that Baton does not crash and lists the Session again.
- [x] After pruning, `baton ls`, `baton watch` and `baton handoff` work as before for the Sessions that remain.
- [x] Tested at the binary seam only (AGENTS.md): Event files with chosen timestamps for ended, lost (dead PID), running, waiting and young Sessions in the host directory and in a Project's `sessions/`; a Handoff and `current.jsonl` that survive; `--dry-run`; the usage errors; a file that cannot be deleted (a read-only directory) gives exit 1 and still processes the others (skip this test if the sandbox of the test run cannot make a directory read-only for its own user, and say so).
- [x] `DESIGN.md`: D7 says that retention is a manual command, what it deletes and what it does not, and the Handoff-link consequence; move "retention" in the v2 table to what is done; README table gets a `baton prune` row with "Stubs only" in the last column.
