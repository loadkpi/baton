# 12: `baton watch` desktop notifications

**What to build:** a stateless Daemon, `baton watch`, running under `systemd --user`, watches Event files and sends a desktop notification when an Agent waits for my approval, when a long turn ends, or when a Session is lost. If it is down, everything else keeps working.

**Blocked by:** 07, 11

**Status:** ready-for-agent

- [ ] On start, `baton watch` reads the current statuses and sends nothing; afterwards it notifies only transitions it sees. It keeps no state on disk (a cursor file is v2).
- [ ] `lost` is noticed by polling every few seconds: the recorded PIDs of host Sessions, one `docker ps` per tick for sandbox Sessions.
- [ ] `waiting_approval` always produces a notification naming the Task and Agent.
- [ ] `waiting_input` produces a notification only if the turn, from `UserPromptSubmit` to `Stop`, took longer than 30 seconds.
- [ ] `lost` always produces a notification.
- [ ] Events from sandbox Sessions are notified from the host; hooks never send notifications.
- [ ] Baton can install and enable the `systemd --user` unit; the command name is chosen here and recorded in `DESIGN.md` D6; tested with a `systemctl` stub.
- [ ] With `baton watch` stopped, `baton ls` and Sessions still work.
- [ ] Tested by running `baton watch` as a process, writing Event files with chosen timestamps after it started, and checking the `notify-send` stub's calls.
