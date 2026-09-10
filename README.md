<p align="center">
  <img src="assets/mark.svg" alt="vector" width="132">
</p>

# vector

Vector is a local model router for AI coding harnesses. It sends the **subagents**
your harness spawns to cost-efficient open models while keeping **frontier
planning** on the Claude and Codex subscriptions you already pay for. One local
gateway resolves every request to a provider, base URL, and API key, so your plan
quota goes to the work that needs it.

> **Beta:** Vector is under active development. Interfaces, configuration, and
> behavior may change between releases. Shape translation between Anthropic and
> OpenAI wire formats is not yet implemented; requests that need it return `501`
> rather than being sent incorrectly. See [Status](#status-and-troubleshooting).

Vector supports macOS and Linux on Apple Silicon, Intel, and ARM.

## Install

One-line installer — downloads the release, verifies its checksum, and installs
to `~/.local/bin`:

```sh
curl -fsSL https://raw.githubusercontent.com/anunay999/vector/main/install.sh | sh
```

With a Go toolchain:

```sh
go install github.com/anunay999/vector/cmd/vector@latest
```

From source:

```sh
git clone https://github.com/anunay999/vector && cd vector
make build          # produces ./bin/vector
```

Confirm the install and see the full guide at any time:

```sh
vector --version
vector guide        # setup guide + current machine state
```

## Set it up with your agent

Paste this into Claude Code, Codex, OpenCode, or any coding agent. It installs
vector, configures it, wires your harnesses, and verifies — no manual steps.

```text
Set up vector, a local model router for coding agents (github.com/anunay999/vector).

1. Install it:
     curl -fsSL https://raw.githubusercontent.com/anunay999/vector/main/install.sh | sh
   If that fails (no release yet), use: go install github.com/anunay999/vector/cmd/vector@latest

2. Read the self-describing guide and current machine state:
     vector guide --json
     vector schema

3. Configure it. Use my OpenRouter key from $OPENROUTER_API_KEY if it is set;
   otherwise ask me for the key before continuing:
     vector setup --key "$OPENROUTER_API_KEY" --wire claude,codex --start --json
   Drop harnesses I don't use from --wire (claude, codex, opencode).

4. Verify and report back:
     vector doctor --json
     vector top --once
   Summarize: config path, which harnesses are wired, gateway status, providers,
   roles, and any warnings. If a check fails, run `vector guide` and fix it.

Rules: never print, log, or commit the API key — it belongs only in
~/.config/vector/env (mode 600). Never edit ~/.codex/config.toml; vector uses a
managed overlay. Subagents must keep running natively in their own harness.

If I already have agents in ~/.claude/agents (or Codex roles), list them with
`vector agents` and tell me which ones still pin a native model, then offer to
route them, e.g. `vector claude route scout`.
```

Or run the whole thing yourself in one command:

```sh
vector setup --key "$OPENROUTER_API_KEY" --wire claude,codex --start --json
```

## Quick start

```sh
vector init                         # config, API key, optional harness wiring
vector up                           # start the local gateway
vector doctor                       # verify config, keys, gateway, and wiring
```

`init` writes `~/.config/vector/config.yaml`, stores your OpenRouter key in the
private env file, and can wire your harnesses. It never overwrites an existing
configuration. `up` starts the gateway in the background; run `vector service
install` to start it at login. `doctor` inspects the full path and reports the
first problem with a concrete fix. Watch it live with `vector top`.

For automation, the same setup runs non-interactively:

```sh
vector setup --key "$OPENROUTER_API_KEY" --wire claude,codex --start --json
```

## Claude Code

```sh
vector claude on
vector claude status
```

`claude on` points new Claude Code sessions at the gateway, enables model
discovery, and installs native subagent definitions under `~/.claude/agents`.
It backs up the values it changes. Planner traffic is reverse-proxied to
Anthropic with your **own** credential; subagent traffic goes to the cheap pool.
Restart Claude Code after enabling or disabling the integration.

Controls:

```sh
vector claude on
vector claude off
vector claude status
```

## Codex CLI

Codex support writes a managed profile overlay and never touches your
`~/.codex/config.toml`. The main session stays fully native; only spawned agents
use the router.

```sh
vector codex on
vector codex status
codex --profile vector
```

Start Codex without `--profile vector` to keep native OpenAI routing. Remove the
overlay with `vector codex off`.

## OpenCode

```sh
vector opencode on
vector opencode status
```

Adds a `vector` provider and per-role subagents to `opencode.json`. Remove them
with `vector opencode off`.

## Agents

Vector installs its own `vector-*` agents and never rewrites the ones you
already have. But an existing agent that pins `model: opus` (Claude Code) or
`model = "..."` (a Codex role file) overrides `CLAUDE_CODE_SUBAGENT_MODEL`, so it
would keep using the native plan. Inspect and repoint them:

```sh
vector agents                              # every agent across harnesses + routed?
vector claude agents
vector claude route scout                  # routes scout -> the scout role
vector claude route worker reviewer        # agent worker -> role reviewer
vector claude route scout --model opus     # or an explicit model / native alias
vector codex  route explorer worker
vector claude route --all --dry-run
```

With no target, an agent is routed to the role of the same name, so you never
write `route scout scout`. `route` edits only the model binding (frontmatter for
Claude, the role file for Codex), backs the file up once, and leaves the prompt
untouched. To force every subagent onto one model instead, set
`force_subagent_model: true` on the harness.

## Dashboard

```sh
vector top
```

A live, refreshing dashboard over local telemetry: 30-minute request and cost
sparklines, a model-mix bar chart, a **14-day cost-per-day histogram**, an
off-plan gauge, per-role latency percentiles and average output tok/s, and the
most recent requests.

Keys: `q` quit, `p` pause, `r` refresh, `f` filter by role, `v` filter by
provider, `a` clear filters. `vector top --once` prints a single frame for
screenshots or CI logs; `--since` and `--interval` tune the window and refresh.

## How it works

A request's model name resolves, strongest signal first, to a
`(provider, base_url, api_key, upstream_model)` triple:

1. an explicit registry model (`openrouter/z-ai/glm-5.3-flash`);
2. an explicit role hint (`vector-worker`, or header `X-Vector-Role`);
3. a `policies` rule matching the harness, traffic class, or model;
4. otherwise native passthrough on your subscription.

Roles are installed as native subagents in each harness, so the frontier model
chooses one by name using its own spawn tool:

| Role | Use for | Default backend |
|---|---|---|
| `vector-architect` | understand, plan, decompose (primary) | native Claude/Codex |
| `vector-lead` | coordinate medium multi-step work | native / GLM-5.3 |
| `vector-reviewer` | code review, tests, PR comments | Kimi-K3 / GLM-5.3 |
| `vector-worker` | scoped edits, mechanical fixes | GLM-5.3-Flash |
| `vector-scout` | read-only search and summarization | DeepSeek-V4-Flash |
| `vector-researcher` | long-context reading | Kimi-K3 / Gemini |
| `vector-escalate` | hard or repeated-failure tasks | native Claude/Codex |

Vector never hosts, sandboxes, or monitors a subagent. Subagents run natively in
the harness; the router only chooses the model and translates wire formats.

## Self-describing CLI

The commands above emit machine-readable output, so both you and an agent can
drive them:

```sh
vector guide --json     # step-by-step guide + live state and next actions
vector schema           # JSON Schema for config.yaml
vector setup --json     # non-interactive one-shot setup
```

Every command supports `--json` (`doctor`, `status`, `models`, `spend`, `guide`,
`setup`), and [AGENTS.md](AGENTS.md) is read automatically by Claude Code, Codex,
and OpenCode.

## Status and troubleshooting

```sh
vector status
vector doctor
vector doctor --json

vector top                       # live dashboard: sparklines, mix bars, roles
                                 # keys: q quit, p pause, r refresh, f role, v provider, a clear
vector top --once                # a single frame
vector logs                      # tail the gateway log
vector logs --follow             # stream it
vector logs --path               # just print the path

vector telemetry --since 24h     # raw request records
vector telemetry --json --limit 0
vector telemetry --role worker --provider openrouter
vector telemetry --follow        # stream new records as JSONL

vector spend --since 24h         # aggregated cost / off-plan share
```

`status` summarizes the gateway, harness wiring, and spend. `doctor` inspects the
same path without changing it and exits non-zero on a failed check. Logs and
telemetry live **beside the active config file** (so `--config /path` keeps them
together):

```text
<config dir>/config.yaml
<config dir>/env                 # secrets, mode 0600
<config dir>/logs/gateway.log    # every start method writes here
<config dir>/telemetry/requests-YYYY-MM-DD.jsonl
```

With the default config that is `~/.config/vector/`. Telemetry records metadata
only — models, provider, role, tokens, cost, latency, status — never prompts,
responses, headers, or bodies.

| Symptom | Fix |
|---|---|
| `unresolved env vars` | `vector env set VAR value` |
| gateway not reachable | `vector up`, then `vector status` |
| harness shows `off` | `vector claude on` / `vector codex on` |
| requests return `501` | provider lacks an Anthropic shape; set `anthropic_base_url` or use OpenRouter |
| Codex children still use the plan | confirm `vector codex on`, then run `codex --profile vector` |

## Configuration

Two files, both mode `0600`:

| File | Holds |
|---|---|
| `~/.config/vector/config.yaml` | providers, models, roles, policies, budget |
| `~/.config/vector/env` | secrets referenced as `${VAR}` |

Prefer the typed commands over direct edits; they validate as they write:

```sh
vector config path
vector config get budget.daily_usd
vector config set budget.daily_usd 10
vector env set OPENROUTER_API_KEY sk-or-...
vector env list                 # values redacted
vector config validate
```

Providers are any OpenAI-compatible endpoint plus native Anthropic/OpenAI.
Roles are ordered preference lists. Daily and per-provider budget ceilings,
concurrency caps, and a fallback chain are all configurable. See
[docs/configuration.md](docs/configuration.md) for every field, or
`vector schema` for the machine-readable schema.

If you run out of subscription credits, the planner can keep working on the cheap
pool: Vector retries the next candidate on a `429`, `5xx`, or a credit/quota
error, and you can force a cheap planner by reordering the `architect` role. See
[docs/configuration.md](docs/configuration.md#degraded-mode-running-the-planner-on-a-cheap-model).

## CLI

```text
vector init | setup | guide | schema
vector up | down | restart | serve | status | doctor
vector config init | show | path | get | set | validate
vector env path | list | set | unset
vector service install | uninstall | status
vector claude | codex | opencode   on | off | status
vector agents [--json]
vector claude | codex   agents | route <agent> [role]
vector models [--json]
vector top [--once] [--since 24h] [--interval 1s]
vector logs [--follow] [--lines N] [--path]
vector telemetry [--since 24h] [--json] [--follow] [--role R] [--provider P]
vector spend [--since 24h] [--json]
```

## Upgrade

```sh
curl -fsSL https://raw.githubusercontent.com/anunay999/vector/main/install.sh | sh
vector restart
vector doctor
```

With Go: `go install github.com/anunay999/vector/cmd/vector@latest`, then
`vector restart`.

## Uninstall

```sh
vector service uninstall
vector down
vector claude off ; vector codex off ; vector opencode off
rm -rf ~/.config/vector
rm -f ~/.local/bin/vector
```

Uninstall never removes your harness credentials or provider API keys from the
providers themselves.

## Privacy and trust

Vector binds its gateway to the local loopback interface. It receives harness
requests and credentials because it is in the selected request path. Request
content leaves the machine only for the upstream chosen by the active routing
policy: native credentials go only to their matching native provider, and
configured provider keys go only to that provider.

Local telemetry contains request metadata — models, provider, role, token
counts, cost, latency, and status — not prompts, responses, headers, or request
bodies. Disable it with `telemetry.enabled: false`. The `/admin/status` endpoint
is restricted to loopback even when the data plane is exposed. Secrets live only
in `~/.config/vector/env` (mode `0600`) and are resolved from the environment
first, then that file.

## Build and test

```sh
make build      # bin/vector
make test       # go test ./...
make race       # go test -race ./...
make vet
make hooks      # install local git hooks
```

Layout and conventions are in [AGENTS.md](AGENTS.md). The design is documented in
[docs/architecture-proposal.md](docs/architecture-proposal.md).

## Contributing

The default branch is protected: no pull request can merge without an approving
review from the code owner (`@anunay999`), enforced via
[`.github/CODEOWNERS`](.github/CODEOWNERS) and GitHub branch protection. Every PR
must also pass the required status checks — `fmt`, `tidy`, `vet`, `build`,
`test`, `lint`, `vuln`, `shellcheck`, and `selfcheck`; CodeQL runs as an advisory
check. Install the local hooks for fast feedback with `make hooks`. See
[docs/governance.md](docs/governance.md). Never commit secrets.

## License

Vector is available under the [Apache License 2.0](LICENSE).
