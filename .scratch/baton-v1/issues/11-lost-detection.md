# 11: `lost` detection

**What to build:** when an Agent's process on the host or its sandbox container dies without `SessionEnd`, `baton ls` shows that Session as `lost` instead of pretending it is still running.

**Blocked by:** 05, 09

**Status:** done

- [x] A host Session whose `agent_pid` no longer runs, or runs with a start time other than `agent_start`, and whose last Event is not `SessionEnd`, is shown as `lost`.
- [x] A sandbox Session whose container no longer exists is shown as `lost`, using the host name recorded in its Events to find the container.
- [x] A Session in a container that still runs, but whose `agent_pid` (in the container's PID namespace) no longer runs there or has another start time, is shown as `lost`; checked with one `docker exec` per running container per check, behind the Runtime interface from 08, so that long-lived containers (the `compose` Runtime) reuse it.
- [x] `lost` is computed on read by the shared status code; nothing is written to Event files.
- [x] A `lost` Session no longer counts as active for the one-active-Session rule.
- [x] Driven by data in Events (a dead PID, a missing container via the `docker` stub); no clock control needed in tests.
