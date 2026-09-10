# AGENTS.md — vector

This file tells AI coding agents how to build, configure, and operate **vector**,
a local model router that sends coding-harness subagents to cheap models while
keeping frontier planning on the user's Claude/Codex subscription.

**If you were asked to "configure this" or "set it up", run the self-describing
guide first:**

```sh
vector guide --json      # full guide + live machine state (paths, gateway, harnesses)
vector schema           # JSON Schema for config.yaml
```

If the binary is not installed yet:

```sh
curl -fsSL https://raw.githubusercontent.com/anunay999/vector/main/install.sh | sh
# or, from source:
go install github.com/anunay999/vector/cmd/vector@latest
```

## Configure it (preferred: one command)

```sh
vector setup --key "$OPENROUTER_API_KEY" --wire claude,codex --start --json
```

`setup` is idempotent: it writes `~/.config/vector/config.yaml` if missing, stores
the key in the private env file, wires the requested harnesses, starts the
gateway, and returns machine-readable checks. Re-run it freely.

Manual equivalent:

```sh
vector config init
vector env set OPENROUTER_API_KEY sk-...
vector claude on
vector codex on
vector up
vector doctor --json
```

## Operating commands

```sh
vector guide [--json]          # agent guide + live state
vector schema                  # config JSON Schema
vector setup --json            # non-interactive one-shot setup
vector doctor --json           # checks; exit 1 on a failed check
vector status --json           # gateway, spend, harness wiring
vector models --json           # roles + model registry
vector spend --json            # usage and estimated cost
vector top [--once]            # live dashboard
vector logs [--follow]         # gateway log
vector telemetry [--json]      # raw request records
vector agents [--json]         # list harness agents + whether routed
vector claude|codex route <agent> [role]  # route an existing agent
vector config get|set|unset <path>  # edit config by dotted path (reloads live)
vector models use <role> <target>    # change a role's model (reloads live)
vector models set|remove <id>        # edit the registry (reloads live)
vector upgrade                       # update the binary + restart the service
vector env set|list|unset            # manage secrets (list redacts)
vector up|down|restart|serve         # gateway lifecycle
vector service install|status        # launchd / systemd --user
```

Config and model edits apply **live** — the running gateway reloads on `SIGHUP`
(sent automatically by the commands above); no restart and no reinstall. Only a
new binary needs `vector upgrade`.

## How configuration works (short version)

Two files, both mode 600:

- `~/.config/vector/config.yaml` — providers, models, roles, policies, budget.
- `~/.config/vector/env` — secrets referenced as `${VAR}`.

A request's model name resolves to `(provider, base_url, api_key, upstream_model)`:

1. explicit registry model (`openrouter/z-ai/glm-5.3-flash`)
2. explicit role hint (`vector-worker`, or header `X-Vector-Role`)
3. a `policies` rule match
4. native passthrough (frontier subscription)

Roles are exposed to harnesses as `vector-<role>`. `architect`/`lead` are
`primary: true` and prefer native providers; subagent roles prefer cheap models.
Providers are any OpenAI-compatible endpoint plus native Anthropic/OpenAI. Native
providers use the caller's own credential (`native: true`); everything else uses
a configured key. If the native plan is out of credits, the next candidate (the
cheap pool) is tried automatically; reorder `roles.architect.prefer` to force a
cheap planner.

Full reference: `docs/configuration.md`. Design: `docs/architecture-proposal.md`.

## Hard constraints (do not violate)

- Subagents run **natively** in the harness. Vector never hosts, sandboxes, or
  monitors an agent, and never routes work through an MCP/agent-host.
- The parent frontier model chooses the subagent role; Vector only resolves the
  model and translates shapes.
- Never write API keys into `config.yaml`; use the env file.
- Never edit the user's `~/.codex/config.toml`; use the managed overlay.

## Build and test

```sh
make build     # bin/vector
make test      # go test ./...
make race      # go test -race ./...
make vet
```

## Repository rules

- The default branch (`main`) is protected. Do not push directly; open a PR. No
  PR merges without code-owner (`@anunay999`) approval.
- Install the local hooks with `make hooks`. See `docs/governance.md`.
- Never commit API keys or secrets.

## Layout

```
cmd/vector            CLI entry point
internal/cli          commands (init, setup, top, logs, telemetry, agents, …)
internal/config       load, validate, defaults, env file
internal/llm          canonical provider-neutral types
internal/registry     model registry
internal/router       role resolution, policies, fallback candidates
internal/provider     upstream request building, auth, streaming
internal/gateway      HTTP surface, usage/cost, budget
internal/stats        aggregation for the dashboard and spend
internal/budget       spend ceilings + concurrency
internal/telemetry    JSONL request store
internal/harness      Claude Code / Codex / OpenCode adapters
internal/service      launchd / systemd install
docs/                 configuration, install, governance, design
```
