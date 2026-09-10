# vector

**Intelligent subagent model router for any harness.**

Vector keeps your frontier model in charge and routes the subagents it spawns to
cheap, capable open models — so your Claude and Codex subscriptions go further
without lowering the quality of the work.

> **Leave the trunk native. Hijack the leaves.**
> Claude Fable/Opus and GPT‑6 Astra do the planning, decomposition, and review.
> GLM‑5.3‑Flash, DeepSeek‑V4‑Flash, and Kimi‑K3 do the execution.

## Who it's for

Developers who already pay for a **Claude subscription and a Codex subscription**
and whose binding constraint is *subscription quota*, not API spend. Subagents
are what burn that quota on mechanical work. Vector sends those subagents to a
pool of cost-efficient models through a single loopback gateway, while the
planner keeps using the plan credential.

The result is measurable: a higher share of subagent tokens off-plan, a lower
cost per completed task, and a low escalation rate — with no drop in merge
quality, because a superior manager compensates for a cheaper worker.

## How it works

Subagents run **natively** in the harness. Vector adds no external runner, no MCP
host, and no sandbox: it installs *named subagent roles* into each harness and
resolves the model each role asks for to a provider, base URL, and API key.

```
                       ┌──────────────────────────────────────────┐
   Claude Code  ──────► │  vector gateway (loopback)               │
   Codex        ──────► │   /v1/messages     (Anthropic)           │
   OpenCode     ──────► │   /v1/chat/completions (OpenAI)          │
                        │   /v1/responses    (OpenAI)              │
                        │                                           │
                        │  classify ─► policy ─► route ─► budget    │
                        └───────┬───────────────┬──────────────┬────┘
                                │               │              │
                         native plan     OpenRouter pool   native API
                       (Anthropic /        (GLM, DeepSeek,  (keys)
                        ChatGPT)            Kimi, Gemini…)
```

The parent model selects a role by name using its **existing** spawn tool
(Claude Code `Task`, Codex `spawn_agent`, OpenCode `task`). The role carries a
virtual model name (`vector-worker`), which the gateway resolves:

| Role | Use for | Default backend |
|---|---|---|
| `vector-architect` | understand, plan, decompose (primary) | native Claude/Codex |
| `vector-lead` | coordinate medium multi-step work | native / GLM‑5.3 |
| `vector-reviewer` | code review, tests, PR comments | Kimi‑K3 / GLM‑5.3 |
| `vector-worker` | scoped edits, mechanical fixes | GLM‑5.3‑Flash |
| `vector-scout` | read-only search and summarization | DeepSeek‑V4‑Flash |
| `vector-researcher` | long-context reading | Kimi‑K3 / Gemini |
| `vector-escalate` | hard or repeated-failure tasks | native Claude/Codex |

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/anunay999/vector/main/install.sh | sh
```

Or with Go: `go install github.com/anunay999/vector/cmd/vector@latest`. See
[docs/install.md](docs/install.md) for Homebrew, Docker, and CI.

## Configure with an AI agent

Vector is self-describing, so you can hand it to a model and say "set this up":

```sh
vector guide --json     # step-by-step guide + live state (config, keys, gateway, harnesses)
vector schema           # JSON Schema for config.yaml, for generating valid config
vector setup --key "$OPENROUTER_API_KEY" --wire claude,codex --start --json
```

`vector setup` is idempotent and non-interactive. Every command supports `--json`
(`doctor`, `status`, `models`, `spend`, `guide`, `setup`), and
[AGENTS.md](AGENTS.md) is auto-read by Claude Code, Codex, and OpenCode.

## Quickstart

```sh
vector init            # guided: config + API key + optional harness wiring
vector up              # start the gateway
vector doctor          # verify config, keys, gateway, and wiring
```

`vector init` is interactive; for automation:

```sh
vector init --yes --key sk-or-... --wire claude,codex
```

Then use Claude Code / Codex as normal. The planner stays on your frontier model;
spawned subagents land on the cheap pool. Verify with:

```sh
vector status     # gateway + harness wiring
vector spend      # requests, tokens, estimated cost, off-plan share
vector models     # roles and the model registry
```

Run the gateway at login:

```sh
vector service install
```

Full configuration guide: [docs/configuration.md](docs/configuration.md).

## Harness wiring

Every adapter is idempotent, backs up once, and removes only the keys it owns.

- **Claude Code** — sets `ANTHROPIC_BASE_URL`, gateway model discovery, and
  `CLAUDE_CODE_SUBAGENT_MODEL`, and installs `.claude/agents/vector-*.md`.
  Planner traffic is reverse-proxied to native Anthropic with the **inbound**
  plan credential; subagent traffic goes to the cheap pool.
- **Codex** — writes a managed `$CODEX_HOME/vector.config.toml` profile and
  `agents/vector-*.toml` role files. The user's `config.toml` is never touched,
  and the main session stays fully native. Run `codex --profile vector`.
- **OpenCode** — adds a `vector` provider and `vector-*` subagents to
  `opencode.json`.

## Configuration

`~/.config/vector/config.yaml` is the single source of truth. Providers are any
OpenAI-compatible endpoint (OpenRouter, Baseten, Z.ai, DeepSeek, Moonshot, …),
plus native Anthropic/OpenAI.

```yaml
providers:
  - id: openrouter
    type: openai_compatible
    base_url: https://openrouter.ai/api/v1
    anthropic_base_url: https://openrouter.ai/api/v1   # Anthropic Messages skin
    api_key: ${OPENROUTER_API_KEY}

models:
  - id: openrouter/z-ai/glm-5.3-flash
    tags: [cheap, fast, tools]
    price: {in: 0.15, out: 0.50}

roles:
  worker:
    tier: cheap
    prefer: [openrouter/z-ai/glm-5.3-flash, openrouter/deepseek/deepseek-v4-flash]

policies:
  - match: {traffic: primary}
    route: architect
  - match: {traffic: subagent}
    route: worker

budget:
  daily_usd: 25
  on_breach: downgrade        # downgrade | queue | stop
```

Precedence: an explicit registry model wins; then an explicit role hint
(`vector-*` or `X-Vector-Role`); then policy; then native passthrough.

## Quota protection

- Subagent routes can never resolve to a plan credential unless a policy says so.
- Per-provider and global daily spend ceilings, with `downgrade`/`queue`/`stop`.
- Transparent fallback on 5xx/429/transport error to the next candidate.
- Per-harness concurrency caps.
- Local JSONL telemetry, with `vector spend` reporting off-plan share.

## CLI

```
vector init [--key K] [--wire claude,codex] [--yes]
vector up | down | restart | status | doctor
vector serve [--verbose]
vector config init | show | path | get | set | validate
vector env path | list | set | unset
vector service install | uninstall | status
vector claude | codex | opencode  on | off | status
vector models [--json]
vector spend [--since 24h] [--json]
```

## Development

```sh
make test     # unit tests
make race     # race detector
make vet      # go vet
make build    # bin/vector
```

Layout:

```
cmd/vector            entry point
internal/config       load, validate, defaults
internal/llm          canonical provider-neutral types
internal/registry     model registry (capabilities, price)
internal/router       role resolution, policies, fallbacks
internal/provider     upstream request building, auth, streaming copy
internal/gateway      HTTP surface for the three wire shapes
internal/budget       spend ceilings and concurrency
internal/telemetry    JSONL request store
internal/harness      Claude Code / Codex / OpenCode adapters
```

## Status

Phase‑0 POC is working and tested: shape-aware routing, model rewrite, configured
vs inbound credential selection, role agents, Codex profile overlay, budget
accounting, fallback retries, and telemetry.

Not yet implemented:

- Shape **translation** (Anthropic ↔ OpenAI Chat/Responses) for providers that
  don't expose an Anthropic-compatible endpoint. Requests that need it return
  `501` rather than being sent incorrectly.
- The benchmark-driven model-intelligence registry and learned routing.
- A status board and Homebrew tap.

See `docs/architecture-proposal.md` for the full design.
