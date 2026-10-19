# 11: `lost` detection

**What to build:** when an Agent's process on the host or its sandbox container dies without `SessionEnd`, `baton ls` shows that Session as `lost` instead of pretending it is still running.

**Blocked by:** 05, 09

**Status:** ready-for-agent

- [ ] A host Session whose process no longer exists and whose last Event is not `SessionEnd` is shown as `lost`.
- [ ] A sandbox Session whose container no longer exists is shown as `lost`, using the host name recorded in its Events to find the container.
- [ ] A `lost` Session no longer counts as active for the one-active-Session rule.
- [ ] Driven by data in Events (a dead PID, a missing container via the `docker` stub); no clock control needed in tests.
