# 16: Compose Runtime and sync into its Profiles

**What to build:** I declare in my Baton config that a Project's Agents run in a service of the Project's own Docker Compose setup (a long-lived dev container with the application), list the Agents' Profiles inside that container with their tags, run `baton sync`, and those Profiles get the same instructions, skills, MCP servers and status hooks as a host Profile. Adds the `compose` Runtime behind the Runtime interface from 08.

**Blocked by:** 08, 15

**Status:** done

- [x] The Baton config can name, per Project, the Compose files, the service, and the Profiles inside the container (Agent, config directory inside the container, tags); the format is chosen here and recorded in `DESIGN.md`. Such a Project's default Runtime is `compose`.
- [x] Before building: check how to write into a Profile that lives in a named Docker volume (`docker compose exec` with a stream into the running container, or a short-lived `docker run` with the volume when the service is stopped), including file ownership for the container's user (a one-shot helper container runs as root); record the finding and the choice in `DESIGN.md`.
- [x] The Profile's real config directory is resolved inside the container (following symlinks, e.g. `~/.claude` pointing into a subdirectory of the volume), not taken from documentation: a prior attempt wrote to the volume root and its hooks silently never ran.
- [x] Nothing depends on tools inside the image (`jq`, `python3`): JSON is merged on the host and only finished files are written; the hooks are the mounted Baton binary.
- [x] Profiles inside the container are full Profiles: instructions, skills, MCP servers and status hooks are written like for a host Profile, with every ownership rule from 02 (manifest, question with diff, backup, no terminal → error) and secrets limited by tags as in 04.
- [x] A Profile in a volume shared by several Compose setups is configured once and synced once.
- [x] Status hooks in these Profiles call the Baton binary at its mount path inside the container with `--runtime compose` and the Profile's name, and write Events into the mounted Project directory.
- [x] Baton never edits the Project's Compose files.
- [x] A stopped service is synced through its volumes without starting or recreating it; a missing volume or container fails for that Profile with a clear error naming the Project and service, and does not stop the other Profiles.
- [x] Tested through the `docker` stub: synced content as received by the stub, ownership rules, shared-volume Profile synced once.
