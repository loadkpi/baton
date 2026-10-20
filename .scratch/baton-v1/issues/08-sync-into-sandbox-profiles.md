# 08: Sync into sandbox Profiles

**What to build:** `baton sync` also renders content into the Claude and Codex Profiles inside every known sandbox Project's `.ai-sandbox/home`, so Codex in the sandbox gets the same instructions, skills, MCP servers and status hooks as Claude on the host. For Codex, Baton writes only fragments, because `ai-sandbox` regenerates the main Codex config and instructions on every run.

**Blocked by:** 04, 05

**Status:** ready-for-agent

- [ ] Every registered Project with Runtime `sandbox` contributes its Claude and Codex Profiles as sync targets, with their own accepted tags; their default tags are chosen here and recorded in `DESIGN.md` D4.
- [ ] Claude in the sandbox gets instructions, skills, MCP servers and status hooks like a host Profile.
- [ ] Codex in the sandbox gets skills and status hooks directly, and instructions and MCP servers only as fragments.
- [ ] Sync never touches files `ai-sandbox` generates or owns (security settings, generated Codex config and instructions).
- [ ] Status hooks in sandbox Profiles call the Baton binary at the path where it is mounted inside the container, with `--runtime sandbox` and the Profile's name, and write Events into the mounted Project directory (`sessions/`).
- [ ] Before building: check whether `run.sh` regenerates Codex `hooks.json` or the skills directory; anything it regenerates gets the fragment treatment.
- [ ] All ownership rules from 02 hold for sandbox Profiles, including the question and backup for shared files.
