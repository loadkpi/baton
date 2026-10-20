# 18: `lost` and Handoff in a Compose container

**What to build:** a Session whose Agent died inside a dev container that is still running shows `lost` and produces a notification, and a Task moves between a host Session and a compose Session with `baton handoff`, in either direction.

**Blocked by:** 17

**Status:** done

- [x] The in-container liveness check from 11 applies to compose Sessions: a running container with a dead or reused `agent_pid` makes the Session `lost`; a gone container does too.
- [x] Container ids change when a container is recreated: Sessions recorded under the old id become `lost`, and new Sessions are tracked under the new id; nothing identifies a container by name only.
- [x] `baton watch` notifies about `lost` compose Sessions like any other, with at most one `docker exec` per running container per tick.
- [x] `baton handoff <task> --to <agent>[@<profile>]` can hand over to and from a compose Profile; the receiving Session starts through the compose Runtime with the Handoff as its first message. (Claude only; Codex in compose is ticket 19. The receiver of a compose Project is always a Claude Profile of its service: a compose Session cannot be handed to a host Profile, there is no `--runtime`.)
- [x] `/baton-handoff` and `baton handoff save` work inside the container: the Task is resolved from the current directory through the mounted Project directory.
- [x] A Handoff to a compose Agent from a Working copy other than the main checkout is refused with an error.
- [x] Tested through the `docker` stub with fixtures for a running container and a dead PID.
