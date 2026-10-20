# 17: `baton new` and `baton ls` in a Compose container

**What to build:** for a Project with Runtime `compose`, `baton new` opens a Guake tab that brings the service up if needed and runs the Agent in it with `docker compose exec`, in the Project's directory inside the container. Sessions there, including ones I start by hand inside the container, show up in `baton ls` in the right Project, Working copy and Task.

**Blocked by:** 16

**Status:** done

- [x] Before starting, Baton checks with `docker inspect` that the container has its two mounts (`~/.baton/projects/<project>` read-write, `~/.baton/bin` read-only). When they are missing, it prints the exact snippet to add to the user's untracked Compose override file and exits non-zero; it never edits Compose files. The message says that adding mounts takes one recreate of the container, which ends the Sessions running in it, so the user picks the moment.
- [x] `baton new` runs `docker compose -f … up -d <service>` when the service is not running, then `docker compose -f … exec -w <project dir in container> <service> <agent>` in a Guake tab titled `<project>/<task>:<agent>`.
- [x] The Project's directory inside the container and the mapping of Event `cwd`s to host paths come from the container's mounts (`docker inspect`), through the Runtime interface; nothing assumes equal paths.
- [x] A compose Session and a host Session in the same main checkout count as the same Working copy for the one-active-Session rule.
- [x] Compose Sessions run only in the Project's main checkout.
- [x] Before building: check whether the Agent inside the container sees API keys from the container's environment and whether `exec` can clear them; record the result, and clear them if possible.
- [x] Running Agent processes keep the hooks they started with; only Sessions started after a sync report Events. When `baton sync` first installs status hooks into a Profile, it says that already running Sessions must be restarted to report Events (this holds for every Runtime and is recorded in `DESIGN.md`).
- [x] `baton ls` shows compose Sessions of both Agents with Runtime `compose` and live status. The status is the one the Events give; whether the Agent is still running (`lost`) is ticket 18, and `baton ls` warns about it meanwhile. Codex Sessions are shown when they report Events (tested with a Codex hook payload); Baton starts only Claude there until ticket 19.
- [x] Tested through the `docker` and `guake` stubs, with `docker inspect` fixtures.
