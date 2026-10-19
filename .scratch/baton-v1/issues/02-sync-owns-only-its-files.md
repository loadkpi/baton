# 02: Sync owns only its files

**What to build:** `baton sync` can be run safely against Profiles that already contain things I did not put there through Baton: Claude's own synced skills, my existing work skills, third-party installs. Sync changes only what its manifest says it owns.

**Blocked by:** 01

**Status:** ready-for-agent

- [ ] Changing a skill in the source updates it in the Profile on the next sync.
- [ ] Removing a skill from the source removes it from the Profile on the next sync, and from the manifest.
- [ ] A foreign file or directory on a path sync wants to write makes sync stop with an error naming that path; nothing is overwritten.
- [ ] Files and directories not in the manifest are never modified or deleted.
- [ ] Sync never writes a file that git tracks, even when a Profile lives inside a git repository.
