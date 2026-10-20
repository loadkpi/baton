# 09: `baton new` starts an Agent in the sandbox

**What to build:** for a Project with Runtime `sandbox`, `baton new` opens a Guake tab that runs the Agent through `ai-sandbox`, with only this Project's Baton directory mounted read-write and the Baton binary mounted read-only, and with Baton's Codex fragments merged into the configs `ai-sandbox` rendered. A Claude Session in the sandbox shows up in `baton ls`. `ai-sandbox` never mentions Baton (ADR 0001).

**Blocked by:** 06, 08

**Status:** ready-for-agent

- [ ] The integration uses a generic `ai-sandbox` mechanism where one makes sense without Baton, otherwise the `AI_SANDBOX_PRINT_ARGS` wrapper; any change to `ai-sandbox` is described in its README without mentioning Baton.
- [ ] The container gets exactly two Baton mounts: this Project's Baton directory read-write and the binary directory read-only (verified through a `docker` stub and a real copy of `run.sh`).
- [ ] Baton's fragments end up between Baton's own markers in the rendered Codex config and instructions; repeated launches do not duplicate them, including when the user removed `ai-sandbox`'s marker.
- [ ] If the image is missing, Baton builds it the way `ai-sandbox` does before launching.
- [ ] Baton's hooks inside a container without the Baton mount do nothing and exit successfully.
- [ ] A Claude Session in the sandbox appears in `baton ls` with Runtime `sandbox` and live status.
- [ ] Sandbox Sessions run only in the Project's main checkout.
- [ ] Paths from sandbox Events (`cwd` inside the container) are mapped to host paths through the container's mounts as recorded by `docker inspect` (or the arguments Baton passed to `docker run`), behind the Runtime interface from 08, never by assuming the paths are equal; the Session resolves to the right Project and Working copy in `baton ls`.
