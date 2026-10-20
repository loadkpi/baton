#!/usr/bin/env bash
set -euo pipefail

MODE="default"
WITH_HOST_SKILLS=false
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CREDS_FILE="${AI_SANDBOX_CREDS_FILE:-$HOME/.config/ai-sandbox/credentials.env}"
HOST_CLAUDE_DIR="${AI_SANDBOX_HOST_CLAUDE_DIR:-$HOME/.claude}"

source "${ROOT}/.ai-sandbox/versions.env"
IMAGE_TAG="ai-sandbox:codex-${CODEX_VERSION}_claude-${CLAUDE_VERSION}"

# Codex model: env override > versions.env > built-in default.
CODEX_MODEL="${AI_SANDBOX_CODEX_MODEL:-${CODEX_MODEL:-gpt-6-astra}}"
if [[ ! "${CODEX_MODEL}" =~ ^[A-Za-z0-9._-]+$ ]]; then
  echo "Invalid Codex model name: ${CODEX_MODEL}" >&2
  exit 1
fi

AI_SANDBOX_MEMORY="${AI_SANDBOX_MEMORY:-4g}"
AI_SANDBOX_CPUS="${AI_SANDBOX_CPUS:-2}"
AI_SANDBOX_PIDS="${AI_SANDBOX_PIDS:-1024}"

usage() {
  cat <<EOF
Usage:
  ./.ai-sandbox/run.sh [--mode default|webfetch|dev|open|offline]
                       [--with-host-skills] [--] [command...]

Modes:
  default   No Web*, no curl/wget. Bridge network (no L3 filter).
  webfetch  Claude WebFetch with host allow-list via net-guard.sh hook.
  dev       Bash + WebFetch with host allow-list; Codex gets network too.
  open      No restrictions (use only for trusted local work).
  offline   --network=none. Maximum isolation; no network at all.

Flags:
  --with-host-skills  Mount the host's ~/.claude/{skills,agents,commands}
                      read-only into the sandbox so your personal skills,
                      subagents and slash-commands are available. Never
                      mounts ~/.claude.json or credentials. Opt-in only.

Env knobs:
  AI_SANDBOX_MEMORY (default 4g)
  AI_SANDBOX_CPUS   (default 2)
  AI_SANDBOX_PIDS   (default 1024)
  AI_SANDBOX_HOST_CLAUDE_DIR  (default \$HOME/.claude; source for --with-host-skills)
  AI_SANDBOX_CODEX_MODEL      (one-off override of CODEX_MODEL from versions.env)

Examples:
  ./.ai-sandbox/run.sh
  ./.ai-sandbox/run.sh -- claude
  ./.ai-sandbox/run.sh --with-host-skills -- claude
  ./.ai-sandbox/run.sh --mode offline -- bash
  ./.ai-sandbox/run.sh --mode dev -- bash -lc 'npm ci && npm test'
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode)
      if [[ $# -lt 2 ]]; then
        echo "--mode requires an argument" >&2
        usage >&2
        exit 1
      fi
      MODE="$2"
      shift 2
      ;;
    --with-host-skills)
      WITH_HOST_SKILLS=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --)
      shift
      break
      ;;
    *)
      break
      ;;
  esac
done

case "${MODE}" in
  default|webfetch|dev|open|offline) ;;
  *)
    echo "Unknown mode: ${MODE}" >&2
    exit 1
    ;;
esac

mkdir -p \
  "${ROOT}/.ai-sandbox/home/.codex" \
  "${ROOT}/.ai-sandbox/home/.config" \
  "${ROOT}/.claude"

render_claude_local_settings() {
  case "${MODE}" in
    default|offline)
      cat > "${ROOT}/.claude/settings.local.json" <<'JSON'
{
  "permissions": {
    "deny": [
      "WebFetch",
      "WebSearch",
      "Bash(curl *)",
      "Bash(wget *)",
      "Bash(ssh *)",
      "Bash(scp *)"
    ]
  }
}
JSON
      ;;
    webfetch)
      cat > "${ROOT}/.claude/settings.local.json" <<'JSON'
{
  "permissions": {
    "deny": [
      "WebSearch",
      "Bash(curl *)",
      "Bash(wget *)",
      "Bash(ssh *)",
      "Bash(scp *)"
    ]
  },
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "WebFetch|WebSearch",
        "hooks": [
          {
            "type": "command",
            "command": "/workspace/.ai-sandbox/hooks/net-guard.sh"
          }
        ]
      }
    ]
  }
}
JSON
      ;;
    dev)
      cat > "${ROOT}/.claude/settings.local.json" <<'JSON'
{
  "permissions": {
    "deny": [
      "WebSearch"
    ]
  },
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash|WebFetch|WebSearch",
        "hooks": [
          {
            "type": "command",
            "command": "/workspace/.ai-sandbox/hooks/net-guard.sh"
          }
        ]
      }
    ]
  }
}
JSON
      ;;
    open)
      cat > "${ROOT}/.claude/settings.local.json" <<'JSON'
{
  "permissions": {
    "deny": []
  }
}
JSON
      ;;
  esac
}

render_codex_config() {
  local target="${ROOT}/.ai-sandbox/home/.codex/config.toml"
  local marker="# managed-by: ai-sandbox/run.sh — DO NOT EDIT (regenerated each run)"

  if [[ -f "${target}" ]] && ! grep -qF "${marker}" "${target}"; then
    echo "[run.sh] preserving user-edited ${target}" >&2
    return 0
  fi

  # Codex's own sandbox (bubblewrap) cannot start inside this container: it
  # needs user namespaces, which --cap-drop=ALL, no-new-privileges and Docker's
  # seccomp profile forbid, and we do not weaken the container for it.
  #   default/webfetch: keep workspace-write. Its network_access = false is the
  #     only thing stopping Codex from using the container's bridge network.
  #   dev/open/offline: danger-full-access. Nothing is lost: dev/open already
  #     give Codex network, offline has none. The container is the boundary.
  local sandbox_mode="workspace-write"
  local workspace_write_block='
[sandbox_workspace_write]
network_access = false
exclude_slash_tmp = true
exclude_tmpdir_env_var = true
'
  if [[ "${MODE}" == "dev" || "${MODE}" == "open" || "${MODE}" == "offline" ]]; then
    sandbox_mode="danger-full-access"
    workspace_write_block=""
  fi

  cat > "${target}" <<EOF
${marker}
model = "${CODEX_MODEL}"
approval_policy = "on-request"
sandbox_mode = "${sandbox_mode}"
web_search = "cached"
${workspace_write_block}
[shell_environment_policy]
inherit = "core"
include_only = ["PATH", "HOME", "USER", "LANG", "LC_ALL", "TERM",
                "GITHUB_TOKEN", "GH_TOKEN", "NPM_TOKEN", "PIP_INDEX_URL"]
EOF
}

# Global Codex instructions ($CODEX_HOME/AGENTS.md). bwrap/unshare cannot work
# in this container, so tell the agent not to try or escalate over it.
render_codex_agents() {
  local target="${ROOT}/.ai-sandbox/home/.codex/AGENTS.md"
  local marker="<!-- managed-by: ai-sandbox/run.sh — DO NOT EDIT (regenerated each run) -->"

  if [[ -f "${target}" ]] && ! grep -qF "${marker}" "${target}"; then
    echo "[run.sh] preserving user-edited ${target}" >&2
    return 0
  fi

  cat > "${target}" <<'EOF'
<!-- managed-by: ai-sandbox/run.sh — DO NOT EDIT (regenerated each run) -->
# Sandbox environment

You are running inside a locked-down Docker container; the container itself is
the security boundary.

- Do not use or try to start bubblewrap (`bwrap`), `unshare`, `chroot`,
  `nsenter` or any other nested sandbox/namespace tool. User namespaces are
  disabled here, so they always fail.
- If a command fails with "No permissions to create a new namespace" or a
  similar sandbox error, do not retry it and do not request escalated
  permissions. Tell the user and continue with a different approach.
- The root filesystem is read-only; write only under /workspace and /tmp.
EOF
}

render_claude_local_settings
render_codex_config
render_codex_agents

if [[ -z "${AI_SANDBOX_PRINT_ARGS:-}" ]] && ! docker image inspect "${IMAGE_TAG}" >/dev/null 2>&1; then
  "${ROOT}/.ai-sandbox/build.sh" >/dev/null
fi

CONTAINER_NAME="ai-sandbox-$(basename "${ROOT}")-$$"

DOCKER_ARGS=(
  run --rm -it
  --name "${CONTAINER_NAME}"
  --user "$(id -u):$(id -g)"
  --workdir /workspace
  --read-only
  --cap-drop=ALL
  --security-opt no-new-privileges
  --pids-limit "${AI_SANDBOX_PIDS}"
  --ulimit nofile=4096:4096
  --memory "${AI_SANDBOX_MEMORY}"
  --memory-swap "${AI_SANDBOX_MEMORY}"
  --cpus "${AI_SANDBOX_CPUS}"
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=1024m
  --tmpfs /run:rw,nosuid,nodev,size=64m
  -e HOME=/home/sandbox
  -e XDG_CONFIG_HOME=/home/sandbox/.config
  -e XDG_CACHE_HOME=/home/sandbox/.cache
  -e XDG_DATA_HOME=/home/sandbox/.local/share
  -e AI_SANDBOX_MODE="${MODE}"
  -e AI_SANDBOX_ALLOWED_DOMAINS_FILE=/workspace/.ai-sandbox/allowed-domains.txt
  -e CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
  -e DISABLE_TELEMETRY=1
  -e DISABLE_ERROR_REPORTING=1
  -e DISABLE_BUG_COMMAND=1
  -v "${ROOT}:/workspace:rw"
  -v "${ROOT}/.ai-sandbox/home:/home/sandbox:rw"
  -v "${ROOT}/.ai-sandbox/run.sh:/workspace/.ai-sandbox/run.sh:ro"
  -v "${ROOT}/.ai-sandbox/build.sh:/workspace/.ai-sandbox/build.sh:ro"
  -v "${ROOT}/.ai-sandbox/update.sh:/workspace/.ai-sandbox/update.sh:ro"
  -v "${ROOT}/.ai-sandbox/check-updates.sh:/workspace/.ai-sandbox/check-updates.sh:ro"
  -v "${ROOT}/.ai-sandbox/Dockerfile:/workspace/.ai-sandbox/Dockerfile:ro"
  -v "${ROOT}/.ai-sandbox/entrypoint.sh:/workspace/.ai-sandbox/entrypoint.sh:ro"
  -v "${ROOT}/.ai-sandbox/versions.env:/workspace/.ai-sandbox/versions.env:ro"
  -v "${ROOT}/.ai-sandbox/hooks:/workspace/.ai-sandbox/hooks:ro"
  -v "${ROOT}/.ai-sandbox/allowed-domains.txt:/workspace/.ai-sandbox/allowed-domains.txt:ro"
  -v "${ROOT}/.claude/settings.json:/workspace/.claude/settings.json:ro"
  -v "${ROOT}/.claude/settings.local.json:/workspace/.claude/settings.local.json:ro"
  -v "${ROOT}/.mcp.json:/workspace/.mcp.json:ro"
)

# Opt-in: surface the host user's personal skills/agents/commands, read-only.
# Deliberately never mounts ~/.claude.json or any credential file.
if [[ "${WITH_HOST_SKILLS}" == "true" ]]; then
  for sub in skills agents commands; do
    if [[ -d "${HOST_CLAUDE_DIR}/${sub}" ]]; then
      DOCKER_ARGS+=( -v "${HOST_CLAUDE_DIR}/${sub}:/home/sandbox/.claude/${sub}:ro" )
      echo "[run.sh] mounting host ~/.claude/${sub} (read-only)" >&2
    fi
  done
fi

case "${MODE}" in
  offline)
    DOCKER_ARGS+=( --network=none )
    ;;
esac

if [[ -f "${CREDS_FILE}" ]]; then
  DOCKER_ARGS+=( --env-file "${CREDS_FILE}" )
fi

if [[ $# -eq 0 ]]; then
  set -- bash
fi

# Testability / debugging hook: print the assembled docker invocation and exit
# instead of running it (used by test/run-args.test.sh).
if [[ -n "${AI_SANDBOX_PRINT_ARGS:-}" ]]; then
  printf '%s\n' docker "${DOCKER_ARGS[@]}" "${IMAGE_TAG}" "$@"
  exit 0
fi

exec docker "${DOCKER_ARGS[@]}" "${IMAGE_TAG}" "$@"
