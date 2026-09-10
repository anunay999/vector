# Configuring vector (agent guide)

You are an AI agent asked to configure **vector**, a local model router that sends
coding-agent subagents to cheap models while keeping frontier planning on the
user's Claude/Codex subscription. Follow this procedure exactly. Prefer
non-interactive commands; do not ask the user unless a required value is missing.

## 0. Ground rules

- Never print, log, or commit API keys. They go only in the env file (mode 600).
- Never edit the user's `~/.codex/config.toml`. Vector uses a separate managed
  overlay.
- Prefer `vector setup` for a one-shot configuration; use the individual commands
  to adjust afterwards.
- Every command below is idempotent unless stated otherwise.

## 1. Inspect current state

```sh
vector guide --json     # machine-readable state + this guide
vector doctor --json    # checks with pass/warn/fail
vector config path      # config file path
vector env path         # secrets file path
```

`vector guide --json` returns `config_exists`, `gateway_running`,
`unresolved_vars`, `providers`, `roles`, and `harnesses`. Use it to decide what
still needs doing.

## 2. One-shot setup (recommended)

```sh
vector setup --key "$OPENROUTER_API_KEY" --wire claude,codex --start
```

Flags:
- `--key <sk-or-...>` — OpenRouter key. If omitted, the existing key in the env
  file is reused, or `$OPENROUTER_API_KEY` is read from the environment.
- `--provider <id>` — provider to configure (default `openrouter`).
- `--wire <list>` — `claude,codex,opencode,all,none` (default `none`).
- `--start` — start the gateway after configuring.
- `--json` — emit a structured result.

## 3. Or step by step

```sh
vector config init                       # write default config.yaml (keeps existing)
vector env set OPENROUTER_API_KEY sk-...  # store the secret (600)
vector config validate                   # confirm it is loadable
vector claude on                         # wire Claude Code
vector codex on                          # wire Codex
vector opencode on                       # wire OpenCode
vector up                                # start the gateway
vector doctor                            # verify end to end
```

## 4. Configure a provider

Any OpenAI-compatible endpoint works. Edit with `vector config set`:

```sh
vector config set providers.1.id baseten
vector config set providers.1.type openai_compatible
vector config set providers.1.base_url https://inference.baseten.co/v1
vector config set providers.1.api_key '${BASETEN_API_KEY}'
vector env set BASETEN_API_KEY <key>
```

`type` is one of `openai_compatible`, `anthropic`, `openai_responses`. Set
`native: true` and no `api_key` to forward the caller's own credential (a
subscription passthrough). `anthropic_base_url` lets an OpenAI-compatible provider
serve Anthropic-shaped requests without translation.

## 5. Configure models and roles

A model id is `<providerID>/<upstreamModel>` (everything after the first slash is
sent upstream, so `openrouter/anthropic/claude-x` works).

```sh
vector config set models.0.id openrouter/z-ai/glm-5.3-flash
vector config set models.0.tags '[cheap, fast, tools]'
vector config set models.0.price.in 0.15
vector config set models.0.price.out 0.50
```

A role is a preference list resolved top to bottom:

```sh
vector config set roles.worker.prefer '[openrouter/deepseek/deepseek-v4.1-flash, openrouter/z-ai/glm-5.3-flash]'
```

Roles are exposed to harnesses as virtual models: `worker` → `vector-worker`.
`architect` and `lead` should be `primary: true` and prefer native providers so
planning stays on the subscription. Keep subagent roles on cheap models.

## 6. Policies, budget, fallback

```sh
vector config set budget.daily_usd 25
vector config set budget.on_breach downgrade          # downgrade | queue | stop
vector config set budget.max_concurrent_subagents_per_harness 8
vector config set fallback.chain '[worker, reviewer, escalate]'
```

Policies are defaults used when a request has no explicit role hint:
`match: {harness, traffic: primary|subagent, model}` → `route: <role>`.

## 7. Verify

```sh
vector doctor --json     # exit 0 when nothing fails
vector status --json
vector models --json
vector spend --json
```

Success criteria: `doctor` reports config ok, no unresolved env vars, gateway
reachable, and each wired harness enabled.

## 8. Secrets

Secrets live only in `~/.config/vector/env` (mode 600) and are referenced from the
config as `${VAR}`. Resolution order is process environment, then the env file.
`vector env list` redacts values.

## 9. Troubleshooting

| Symptom | Fix |
|---|---|
| `unresolved env vars` | `vector env set VAR value` |
| gateway not reachable | `vector up`, then `vector status` |
| harness shows `off` | `vector claude on` / `vector codex on` |
| requests return 501 | provider lacks an Anthropic shape; set `anthropic_base_url` or use OpenRouter |
| Codex children still use the plan | confirm `codex on` then run `codex --profile vector` |
| subagent not on cheap model | check the role's `prefer` list and `vector models` |

## 10. Full reference

- Configuration: `docs/configuration.md`
- Install: `docs/install.md`
- Design: `docs/architecture-proposal.md`
- Config JSON Schema: `vector schema`
