# 13: Telegram while away

**What to build:** `baton away` routes notifications to Telegram until `baton back`, so I learn about waiting Agents when I am away from the desk. Messages carry only the status and the Task name.

**Blocked by:** 12

**Status:** ready-for-agent

- [ ] After `baton away`, notifications go to Telegram instead of the desktop; after `baton back`, to the desktop again.
- [ ] The Telegram bot token and chat id live in Baton's local config and secrets on the host and never reach a sandbox.
- [ ] Messages contain only status, Task name and Agent; never command output or Event payloads.
- [ ] Sending is one-way; incoming Telegram messages are ignored.
- [ ] Tested against a local fake Telegram HTTP server configured by URL.
