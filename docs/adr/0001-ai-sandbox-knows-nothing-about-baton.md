# ai-sandbox knows nothing about Baton

The `sandbox` Runtime in Baton is the existing `ai-sandbox` project (public `loadkpi/ai-sandbox`), not a custom image. The sandbox stays a standalone tool that works without Baton: it never mentions Baton, and Baton's hooks inside the container silently do nothing when `~/.baton` is not mounted.

Baton plugs into the sandbox in two ways, in this order of preference:

1. **A generic mechanism in `ai-sandbox` itself**, when it makes sense without Baton, i.e. it can be described in the `ai-sandbox` README without mentioning Baton. Examples: extra mounts (`--mount host:container[:ro]`), fragments for generated configs (`config.d/`, `AGENTS.d/`).
2. **Otherwise, a wrapper around `AI_SANDBOX_PRINT_ARGS`**: Baton calls `run.sh` in print-args mode (configs are already rendered at that point), adds its own block between its own markers to the configs and its own arguments to `docker`, then runs `docker` itself.

## Considered Options

- **A custom image with both CLIs (original D2 in DESIGN.md).** Rejected: the sandbox already exists, is proven, and carries a security model (modes, network hook, read-only rootfs) that would have to be rebuilt.
- **Direct integration: `run.sh` knows about `~/.baton` and `baton hook`.** Rejected: the sandbox would stop being standalone, and every Baton change would require editing every vendored copy of `.ai-sandbox/`.
- **A private `ai-sandbox` branch with Baton-specific changes.** Rejected: constant porting from the public `main`, while generic mechanisms can live in the public version anyway.

## Consequences

- Config ownership inside the sandbox is split: `ai-sandbox` owns security (permissions, `sandbox_mode`, network), `baton sync` owns content (instructions, skills, MCP, status hooks).
- `AI_SANDBOX_PRINT_ARGS` and its output format (one argument per line) become a contract Baton relies on; change it with care.
