# 18: `lost` and Handoff in a Compose container

**What to build:** a Session whose Agent died inside a dev container that is still running shows `lost` and produces a notification, and a Task moves between a host Session and a compose Session with `baton handoff`, in either direction.

**Blocked by:** 17

**Status:** ready-for-agent

- [ ] The in-container liveness check from 11 applies to compose Sessions: a running container with a dead or reused `agent_pid` makes the Session `lost`; a gone container does too.
- [ ] Container ids change when a container is recreated: Sessions recorded under the old id become `lost`, and new Sessions are tracked under the new id; nothing identifies a container by name only.
- [ ] `baton watch` notifies about `lost` compose Sessions like any other, with at most one `docker exec` per running container per tick.
- [ ] `baton handoff <task> --to <agent>[@<profile>]` can hand over to and from a compose Profile; the receiving Session starts through the compose Runtime with the Handoff as its first message.
- [ ] `/baton-handoff` and `baton handoff save` work inside the container: the Task is resolved from the current directory through the mounted Project directory.
- [ ] A Handoff to a compose Agent from a Working copy other than the main checkout is refused with an error.
- [ ] Tested through the `docker` stub with fixtures for a running container and a dead PID.
