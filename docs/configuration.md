# Configuration

Vector has two files. Everything else is derived.

| File | Purpose | Permissions |
|---|---|---|
| `~/.config/vector/config.yaml` | providers, models, roles, policies, budget | `600` |
| `~/.config/vector/env` | secrets referenced as `${VAR}` in the config | `600` |

Override the directory with `VECTOR_CONFIG_DIR`. Find the active paths with:

```sh
vector config path
vector env path
```

Edit by hand or with the CLI:

```sh
vector init                                   # guided setup
vector config get budget.daily_usd
vector config set budget.daily_usd 10
vector config set providers.0.api_key '${OPENROUTER_API_KEY}'
vector env set OPENROUTER_API_KEY sk-or-...
vector env list                               # values redacted
vector config validate
```

## The mental model

A request arrives with a **model name**. Vector turns that into a
**(provider, base URL, API key, upstream model)** triple:

```
request model ──► role? ──► provider preference list ──► provider
   vector-worker     worker      [glm-flash, deepseek]     openrouter
                                                          base_url=https://openrouter.ai/api/v1
                                                          api_key=${OPENROUTER_API_KEY}
                                                          model=z-ai/glm-5.3-flash
```

Precedence, strongest first:

1. **Explicit registry model** — the request names `openrouter/z-ai/glm-5.3-flash`.
2. **Explicit role hint** — the request names `vector-worker`, or sends
   `X-Vector-Role: reviewer`.
3. **Policy** — a `policies` rule matches the harness/traffic/model.
4. **Native passthrough** — anything else goes to the native provider for the
   wire shape, unchanged.

## Providers

Any OpenAI-compatible endpoint plus native Anthropic/OpenAI.

```yaml
providers:
  - id: openrouter
    type: openai_compatible          # openai_compatible | anthropic | openai_responses
    base_url: https://openrouter.ai/api/v1
    anthropic_base_url: https://openrouter.ai/api/v1   # optional Anthropic Messages skin
    api_key: ${OPENROUTER_API_KEY}
    headers:
      HTTP-Referer: https://github.com/anunay999/vector
```

| Field | Meaning |
|---|---|
| `id` | Namespaced in models (`<id>/<model>`) and referenced by roles |
| `type` | `openai_compatible`, `anthropic`, or `openai_responses` |
| `base_url` | Upstream root; version segments are not duplicated when joined |
| `anthropic_base_url` | Extra Anthropic-shape root so Anthropic requests skip translation |
| `api_key` | Literal or `${VAR}`; empty and `native: true` means "use the inbound credential" |
| `default_model` | Used when a virtual role targets a native provider (e.g. `vector-escalate`) |
| `native` | Forward the caller's own Authorization/X-Api-Key (subscription passthrough) |
| `headers` | Static headers merged into every upstream request |

Common providers:

```yaml
  - {id: baseten,   type: openai_compatible, base_url: https://inference.baseten.co/v1, api_key: ${BASETEN_API_KEY}}
  - {id: zai,       type: openai_compatible, base_url: https://api.z.ai/api/paas/v4,     api_key: ${ZAI_API_KEY}}
  - {id: deepseek,  type: openai_compatible, base_url: https://api.deepseek.com/v1,      api_key: ${DEEPSEEK_API_KEY}}
  - {id: moonshot,  type: openai_compatible, base_url: https://api.moonshot.ai/v1,       api_key: ${MOONSHOT_API_KEY}}
  - {id: anthropic-native, type: anthropic,        base_url: https://api.anthropic.com, default_model: claude-opus-5, native: true}
  - {id: openai-native,    type: openai_responses, base_url: https://api.openai.com/v1,  default_model: gpt-6-astra,  native: true}
```

## Models registry

Register each routable model once. `id` is `<providerID>/<upstreamModel>`; the
part after the first slash is sent upstream, so `openrouter/anthropic/claude-x`
works.

```yaml
models:
  - id: openrouter/z-ai/glm-5.3-flash
    tags: [cheap, fast, tools, long_context]
    context: 1310720
    price: {in: 0.15, out: 0.50}      # USD per million tokens
```

`price` powers the cost estimate and budget; `tags` are used by the model
registry to pick the cheapest model that satisfies a task.

## Roles

A role is a preference list. Vector walks it and takes the first target that can
serve the request's wire shape.

```yaml
roles:
  architect:                     # planning — stays on the subscription
    tier: frontier
    primary: true                # primary traffic never consumes a subagent slot
    prefer: [anthropic-native, openai-native]
  reviewer:
    tier: smart
    prefer: [openrouter/moonshotai/kimi-k3, openrouter/z-ai/glm-5.3]
  worker:
    tier: cheap
    prefer: [openrouter/deepseek/deepseek-v4.1-flash, openrouter/z-ai/glm-5.3-flash]
  escalate:
    tier: frontier
    prefer: [anthropic-native, openai-native]
```

A `prefer` entry can be another role (chained), a registered model, or a
provider id. Native provider entries use `default_model` when the incoming model
is virtual.

Roles are exposed to harnesses as virtual models: role `worker` → `vector-worker`
(alias `vector/worker`). `vector-auto` resolves to `complexity.default_floor`.

## Policies

Policies are defaults applied when there is no explicit role hint. Empty fields
are wildcards; `model` supports a trailing `*`.

```yaml
policies:
  - {match: {traffic: primary},  route: architect}
  - {match: {traffic: subagent}, route: worker}
  - {match: {harness: codex, traffic: subagent}, route: reviewer}
  - {match: {model: "vector-*"}, route: worker}
```

## Model redirects (`model_map`)

`model_map` forces a concrete inbound model to another target — the "say it like
a sentence" redirect. It is an ordered table; the first matching `from` wins,
`from` supports a trailing `*`, and `to` is a role, a registry model, or a
provider. It takes precedence over policies, but not over an explicit registry
model or a `vector-*` role.

```yaml
model_map:
  - {from: claude-sonnet-5, to: openrouter/z-ai/glm-5.3-flash}
  - {from: "claude-opus*",  to: openrouter/deepseek/deepseek-v4.1-flash}
```

```sh
vector models map claude-sonnet-5 openrouter/z-ai/glm-5.3-flash
vector models map "claude-opus*"  openrouter/deepseek/deepseek-v4.1-flash
vector models map                 # list
vector models map --remove claude-sonnet-5
```

Note this applies to whatever traffic names that model — including the main
session if it runs `claude-opus-5`. Use it deliberately: it moves the "trunk",
not just the leaves.

## Budget and fallback

```yaml
budget:
  daily_usd: 25
  per_provider: {openrouter: 20, deepseek: 5}
  on_breach: downgrade              # downgrade | queue | stop
  max_concurrent_subagents_per_harness: 8

fallback:
  cooldown: 30s
  ttft_timeout: 30s
  chain: [worker, reviewer, escalate]
```

- `downgrade` tries the cheaper fallback chain before the frontier choice.
- `queue` returns `429` with `Retry-After`; `stop` rejects.
- On upstream `5xx`/`429`/transport error, Vector retries the next candidate in
  `chain` transparently.
- Native (subscription) providers have no `price`, so they never count toward
  the dollar ceiling — they consume quota, which is the whole point.

## Secrets

Never put keys in `config.yaml`. Reference them:

```yaml
api_key: ${OPENROUTER_API_KEY}
```

Resolution order: process environment first, then `~/.config/vector/env`. Store
secrets privately:

```sh
vector env set OPENROUTER_API_KEY sk-or-...
chmod 600 ~/.config/vector/env
vector env list          # shows OPENROUTER_API_KEY=sk-or-…abcd
```

## Harnesses

```yaml
harnesses:
  claude-code: {enabled: true, subagent_model: vector-worker}
  codex:       {enabled: true, profile: vector}
  opencode:    {enabled: true}

telemetry: {dir: ~/.config/vector/telemetry, retention_days: 90, enabled: true}
```

## Recipes

**OpenRouter-first (default).** One key routes every subagent to the cheap pool;
the planner passes through to native Anthropic with your subscription.

**Direct providers (no aggregator).** Point Claude Code subagents at Z.ai or
Moonshot directly. Claude Code needs an Anthropic-shaped target; set
`anthropic_base_url` if the provider offers one, otherwise translation is
required (not yet implemented) and the request returns `501`.

**Baseten for the manager.** Add Baseten's OpenAI-compatible endpoint as a
provider and put a `baseten/...` model in the `lead` role, so medium work runs on
Baseten while hard work escalates to native Claude.

**CI / headless.** `vector serve` in a container, keys from environment
variables, `daily_usd` for a hard ceiling, `on_breach: stop`.

## Degraded mode: running the planner on a cheap model

If you run out of Claude credits, the *planner* can run on a cheap model too —
Claude Code stays pointed at Vector (that is what `vector claude on` does), and
Vector decides where the main-session traffic goes.

**Automatic.** Keep native first and let Vector fail over. On a `429`, `5xx`, or
a credit/quota error from the native provider, Vector retries the next candidate
in the role's preference list and the `fallback.chain`. With the default chain
(`worker → reviewer → escalate`), a planner that is out of credits lands on
GLM‑5.3‑Flash / Kimi‑K3 automatically and the session keeps going:

```yaml
roles:
  architect:
    prefer: [anthropic-native, openrouter/z-ai/glm-5.3, openrouter/moonshotai/kimi-k3]
fallback:
  chain: [worker, reviewer, escalate]
```

**Forced.** To run the planner on a cheap model unconditionally, put it first:

```sh
vector config set roles.architect.prefer \
  '[openrouter/z-ai/glm-5.3, openrouter/moonshotai/kimi-k3, anthropic-native]'
vector restart
```

Then verify with `vector spend` (the architect rows should show the cheap
provider) and `vector doctor`.

Caveats: a cheap model as the main agent is a *degraded* mode — multi-step tool
use and long-context reliability are weaker than Claude/Codex, and harness
features that assume Anthropic semantics may behave differently. Prefer it as a
fallback rather than the everyday default.

For **Codex**, the main session is native unless you route it through Vector.
Add a top-level provider to the managed overlay or run the main session under
the `vector` profile with its provider set, then the same fallback applies.

## Existing agents in your harness

Vector installs its own `vector-*` agents and never rewrites yours. But an
existing agent that pins `model: opus` (Claude Code) or `model = "..."` (a Codex
role file) **overrides** `CLAUDE_CODE_SUBAGENT_MODEL`, so it would keep using the
native plan. Point it at a virtual model to route it through Vector:

```sh
vector agents                                   # what exists, and whether it is routed
vector claude route scout                        # -> the scout role (no target needed)
vector claude route worker reviewer              # agent worker -> role reviewer
vector claude route scout --model opus           # explicit model / native alias
vector codex  route explorer worker
vector claude route --all --dry-run               # every non-vector agent
```

With no target, an agent routes to the role of the same name. `route` edits only
the model binding (frontmatter for Claude, the role file for Codex), backs the
file up once, and leaves the prompt intact.

If you would rather force every subagent onto one model without editing agents,
set `force_subagent_model` on the harness; it writes
`CLAUDE_CODE_SUBAGENT_MODEL_FORCE` and overrides per-agent pinning.

## Logs and telemetry

Both live beside the active config file (`--config /path` keeps them together;
the default is `~/.config/vector/`).

| What | Where | See it |
|---|---|---|
| Gateway log | `<config>/logs/gateway.log` | `vector logs [-f] [--path]` |
| Request telemetry | `<config>/telemetry/requests-YYYY-MM-DD.jsonl` | `vector telemetry`, `vector spend` |

`vector up`, `vector serve`, and the installed service all write the same
`gateway.log`. The service previously used a separate location; `vector service
install` now points launchd/systemd at this file, and Linux no longer requires
the journal.

Telemetry is append-only JSONL, one record per routed request: time, request id,
harness, role, requested/routed model, provider, shapes, stream, status, latency,
tokens, estimated cost, reason, and error. Read it with:

```sh
vector telemetry --since 1h --limit 0        # raw table
vector telemetry --json                      # machine output
vector telemetry --follow                    # stream new records
vector telemetry --role worker --provider openrouter
vector spend --since 168h                    # aggregate
```

It records metadata only — never prompts, responses, headers, or bodies. Disable
it with `telemetry.enabled: false`; set `telemetry.dir` to relocate it.

## Applying changes

Config and model edits apply **live**: the running gateway reloads on `SIGHUP`,
and `vector config set` / `unset` and `vector models set` / `use` / `remove` send
it automatically. No restart, no reinstall — in-flight requests keep their
snapshot.

```sh
vector models use worker openrouter/deepseek/deepseek-v4.1-flash
vector config set budget.daily_usd 15
```

Only a **new binary** requires an update:

```sh
vector upgrade      # re-runs the installer, then restarts the service
```

If the gateway is not running (e.g. `vector serve` in the foreground), edits take
effect the next time it starts.

## Verify

```sh
vector config validate
vector doctor
vector models
vector spend --since 24h
```
