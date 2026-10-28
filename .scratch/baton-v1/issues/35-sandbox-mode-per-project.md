# 35: ai-sandbox mode per Project

**What to build:** a Project can name the `ai-sandbox` mode Baton starts its sandbox in (`run.sh --mode <mode>`), and `baton new`/`baton handoff` warn when the Codex config that `run.sh` rendered leaves Codex unable to run shell commands.

**Blocked by:** none

**Status:** done

**Why.** Found on Oct 28, 2026, preparing a Claude → Codex Handoff in a sandbox: Baton runs `bash .ai-sandbox/run.sh -- env -u ... <agent>` with no `--mode`, so `run.sh` always uses its `MODE="default"`. In `ai-sandbox` (its ADR-015) the `default` and `webfetch` modes render Codex's `config.toml` with `sandbox_mode = "workspace-write"`, and Codex's own sandbox (bubblewrap) cannot start inside the container (`bwrap: No permissions to create a new namespace`), so every shell command of a Codex started by Baton fails, including reading a Handoff. `dev`, `open` and `offline` render `danger-full-access`, which works. Older copies of `run.sh` in Projects render `workspace-write` in every mode; they accept `--mode` too. Baton must not learn `ai-sandbox`'s modes (ADR 0001); passing `--mode` and reading the rendered file it already merges into are generic.

- [x] Config: `[projects.<name>] sandbox_mode = "<mode>"` (the value of `run.sh --mode`; a non-empty word of `[a-z0-9-]`, anything else is a config error naming the Project; which modes exist is `run.sh`'s business, an unknown one fails there and its message is shown). Allowed only in the table of a Project whose Runtime is `sandbox`; in a Compose Project's table it is an error. Unset means no `--mode`, as now.
- [x] `baton new` and `baton handoff` for a sandbox Project pass `--mode <mode>` to `run.sh` before `--`, in the print-args run, so the rendered configuration and the printed `docker run` command are those of that mode.
- [x] After the print-args run, when the Agent is Codex and the rendered `<home>/.codex/config.toml` has `sandbox_mode = "workspace-write"`, print a warning on stderr: Codex's own sandbox cannot start inside the container, so its shell commands will fail; set `sandbox_mode = "dev"` (or another mode whose `run.sh` gives `danger-full-access`) under `[projects.<name>]`, or update `.ai-sandbox/run.sh`. The launch still goes ahead. No warning for Claude, or when the key is missing or has another value.
- [x] Tests at the binary seam with the fake `run.sh` the sandbox tests already use: `--mode` passed when set and absent when not; a bad value and the key in a Compose Project's table are config errors; the warning for Codex with `workspace-write`, none with `danger-full-access`, none for Claude; `baton handoff` to Codex passes the mode too.
- [x] README: the key in the config example and one line on why Codex needs it. DESIGN.md: next to the sandbox launch (ticket 09) and the per-Project table, with the reason above (ticket 35).
