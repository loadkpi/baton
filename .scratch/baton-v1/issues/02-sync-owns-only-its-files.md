# 02: Sync owns only its files

**What to build:** `baton sync` can be run safely against Profiles that already contain things I did not put there through Baton: Claude's own synced skills, my existing work skills, third-party installs, my own `CLAUDE.md` and `settings.json`. Sync changes only what its manifest says it owns, and before it first adds its part to a file it does not own, it shows the diff, asks me and backs the file up. This ticket builds that mechanism; 03, 04 and 05 use it for instructions, MCP servers and hooks.

**Blocked by:** 01

**Status:** ready-for-agent

- [ ] Changing a skill in the source updates it in the Profile on the next sync.
- [ ] Removing a skill from the source removes it from the Profile on the next sync, and from the manifest.
- [ ] Files and directories not in the manifest are never modified or deleted, except through the question below.
- [ ] Sync can own a part of a shared file: a block between `<!-- baton:begin -->` and `<!-- baton:end -->` in Markdown, named keys in JSON and TOML. The manifest records which part it owns.
- [ ] The first change to a shared file shows the diff and asks; on yes the file is copied to `~/.baton/backups/<profile>/<ts>/` and written atomically (temp file and rename), and everything outside Baton's part stays byte-for-byte the same; on no, sync stops with an error naming the path.
- [ ] Later syncs update Baton's part without asking; if Baton's part was edited by hand, sync asks again.
- [ ] A foreign skill directory with the same name as a source skill gets the same question and backup before it is replaced.
- [ ] Without a terminal to ask in, sync stops with an error naming the path, and nothing is written.
- [ ] Sync never writes a file that git tracks, even when a Profile lives inside a git repository.
- [ ] Answers are tested through a pseudo-terminal; the stop is tested with stdin that is not a terminal.
