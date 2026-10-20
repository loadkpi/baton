# 03: Instructions and tags across host Profiles

**What to build:** my source `AGENTS.md` reaches every host Claude Profile as its instructions, and tags decide which skills reach which Profile, so work content goes to `claude-work` and not to `claude-personal`.

**Blocked by:** 02

**Status:** ready-for-agent

- [ ] The source `AGENTS.md` is rendered as Baton's block in the instructions file (`CLAUDE.md`) of every configured host Claude Profile; an existing `CLAUDE.md` gets the block through the shared-file flow from 02.
- [ ] Each Profile in the Baton config has a list of tags it accepts.
- [ ] How items carry tags in the source (for example, frontmatter in `SKILL.md`) is chosen here and recorded in `DESIGN.md` D4.
- [ ] A tagged item reaches only Profiles that accept at least one of its tags.
- [ ] Untagged instructions and skills reach every Profile.
- [ ] Changing a Profile's tags and re-running sync removes items it no longer accepts (owned files and owned parts only).
