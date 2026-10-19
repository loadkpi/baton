# 01: Sync skills into one host Claude Profile

**What to build:** I point Baton at my private source repository, add a skill there, run `baton sync`, and the skill appears in my `claude-personal` Profile. This is the first tracer bullet: it brings up the Go module, the single `baton` binary, the Baton config (source repository path, host Profiles), the owned-files manifest, and the test harness every later ticket reuses (build the binary, run it as a subprocess with an isolated `HOME`, stub executables on `PATH`).

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] `baton sync` copies every skill from the source into the configured host Claude Profile in the layout Claude Code reads.
- [ ] Every file sync writes is recorded in the manifest for that Profile.
- [ ] Running `baton sync` twice without source changes changes nothing.
- [ ] A missing or invalid Baton config gives a clear error and a non-zero exit code.
- [ ] The test harness runs `baton` as a subprocess against an isolated `HOME` and a temporary source repository; no test touches the real home directory.
