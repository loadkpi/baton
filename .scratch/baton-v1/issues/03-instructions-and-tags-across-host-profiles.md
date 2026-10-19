# 03: Instructions and tags across host Profiles

**What to build:** my source `AGENTS.md` reaches every host Claude Profile as its instructions, and tags decide which skills reach which Profile, so work content goes to `claude-work` and not to `claude-personal`.

**Blocked by:** 01

**Status:** ready-for-agent

- [ ] The source `AGENTS.md` is rendered as the instructions file of every configured host Claude Profile.
- [ ] Each Profile in the Baton config has a list of tags it accepts.
- [ ] A tagged item reaches only Profiles that accept at least one of its tags.
- [ ] Untagged instructions and skills reach every Profile.
- [ ] Changing a Profile's tags and re-running sync removes items it no longer accepts (owned files only).
