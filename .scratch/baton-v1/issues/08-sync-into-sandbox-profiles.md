# 08: Sync into sandbox Profiles

**What to build:** `baton sync` also renders content into the Claude and Codex Profiles inside every known sandbox Project's `.ai-sandbox/home`, so Codex in the sandbox gets the same instructions, skills, MCP servers and status hooks as Claude on the host. For Codex, Baton writes only fragments, because `ai-sandbox` regenerates the main Codex config and instructions on every run.

**Blocked by:** 04, 05

**Status:** done

- [x] Introduces the Runtime interface from the spec (start an Agent, locate and write Profiles, map a container path to a host path, check that a Session's Agent is alive); `host` and `sandbox` implement it, sync and later commands go through it, and the `compose` Runtime (16–18) can plug in without changing callers. Only the parts this ticket uses are implemented; the rest of the interface is declared and filled in by 09 and 11.
- [x] Every registered Project with Runtime `sandbox` contributes its Claude and Codex Profiles as sync targets, with their own accepted tags; their default tags are chosen here and recorded in `DESIGN.md` D4.
- [x] Claude in the sandbox gets instructions, skills, MCP servers and status hooks like a host Profile.
- [x] Codex in the sandbox gets skills and status hooks directly, and instructions and MCP servers only as fragments.
- [x] Sync never touches files `ai-sandbox` generates or owns (security settings, generated Codex config and instructions).
- [x] Status hooks in sandbox Profiles call the Baton binary at the path where it is mounted inside the container, with `--runtime sandbox` and the Profile's name, and write Events into the mounted Project directory (`sessions/`).
  - Sync writes the hooks with the binary path `/baton/bin/baton`, `--runtime sandbox` and the Profile name (`TestSandboxSync_*`); making `baton hook --runtime sandbox` write its Events into `/baton/project/sessions/` (and do nothing without that mount) was done in ticket 09 (`TestSandboxHook_*`).
- [x] Before building: check whether `run.sh` regenerates Codex `hooks.json` or the skills directory; anything it regenerates gets the fragment treatment.
- [x] All ownership rules from 02 hold for sandbox Profiles, including the question and backup for shared files.
