# Vector — Intelligent subagent model router for any harness

**Status:** proposal / design discussion
**Reference implementation studied:** `baseten-switch` v0.3.0 (installed locally), plus `claude-code-router` (CCR).

---

## 0. Who it's for and why it works

**Primary user:** a developer who already pays for a **Claude subscription and a
Codex (ChatGPT) subscription**, and whose binding constraint is *subscription
quota*, not API spend. Their frontier models are dramatically better than any
open model; their subagents are what burn the quota on mechanical work.

**The deal:** keep the frontier model as the manager/orchestrator — it understands
the issue, decomposes it, writes a precise brief, and verifies the result — but
send the *execution* subagents to cost-efficient open models (GLM‑5.3‑Flash,
DeepSeek‑V4‑Flash, Kimi‑K3). The subscription stretches much further; the work
still lands because a superior manager compensates for a cheaper worker.

**Why quality holds:** delegation quality, not raw worker IQ, determines the
outcome. A Fable/Astra/Opus parent can:
- define scope and acceptance criteria the worker can actually satisfy,
- hand over exactly the context and tools the task needs,
- check the result and re-brief or escalate on failure.

Vector's role is to make that handoff frictionless **and** to ship the delegation
contract (role prompts, brief/acceptance-criteria conventions, verification loops,
escalation), not just the routing.

**Success metrics:** % of subagent tokens off-plan; cost per *completed* task;
escalation rate (should stay low); frontier-quota saved per week with no drop in
merge quality.

**Cost model note:** the trade is sunk subscription quota vs marginal API spend.
For a subscriber, moving a subagent off-plan is usually worth a few cents of
OpenRouter credit, especially at GLM‑5.3‑Flash / DeepSeek‑V4‑Flash prices. Vector
should show both sides: quota preserved and dollars spent.

### One critical unknown to validate first

**Does Claude Code's subscription OAuth survive a proxy round-trip?** Claude Code
shares a single `ANTHROPIC_BASE_URL` for planner and subagents, so to route
subagents elsewhere Vector must proxy *all* Claude Code traffic and pass the
planner request through to native Anthropic with the **inbound credential**. If
subscription OAuth is rejected at `api.anthropic.com` when relayed through a
third-party process, the fallback is an API key for the planner (which loses the
subscription benefit) — so this is a Phase‑0 spike, not an assumption.
Codex has no such risk: its main session can stay fully native and only child
role configs point at Vector.

---

## 1. The one idea

> **Leave the trunk native. Hijack the leaves.**

The expensive planning work stays on the frontier models the user already pays for
(Claude Fable / Opus in Claude Code, GPT‑6 Astra / Sol in Codex). Vector only
intercepts the **subagent / leaf traffic** those harnesses spawn, and routes it to
a pool of cheap, capable models (GLM‑5.3‑Flash, DeepSeek‑V4‑Flash, Kimi‑K3, …)
through any OpenAI‑compatible endpoint — OpenRouter first.

Vector is not a Claude replacement and not a "run Claude Code on cheap models"
tool. It is a **control plane for delegation**: frontier orchestrates, cheap
intelligence executes, and the router decides which cheap model, when, and when to
escalate back up.

```
                        ┌──────────────────────────────────────────┐
   Claude Code  ──────► │  Vector gateway (loopback)               │
   Codex        ──────► │   /v1/messages   (Anthropic)             │
   OpenCode     ──────► │   /v1/responses  (OpenAI Responses)      │
   (any harness)        │   /v1/chat/completions (OpenAI Chat)     │
                        │                                           │
                        │   classify ─► policy ─► route ─► budget   │
                        └───────┬───────────────┬──────────────┬────┘
                                │               │              │
                         native plan     OpenRouter pool   native API
                       (Anthropic/         (GLM, DeepSeek,  (Anthropic/
                        ChatGPT)            Kimi, Qwen…)     OpenAI keys)
```

---

## 1a. The delegation contract — parent decides, harness runs natively

Hard requirements:

- Subagents must run **inside** the parent harness (Claude Code `Task`, Codex
  `spawn_agent`, OpenCode `task`). **No external runner, no MCP/agent-host, no
  sandbox daemon, no side-channel monitor.**
- The **parent** model (Fable / Astra / Opus) decides task complexity and which
  subagent to use.
- Vector resolves that choice to the **right provider, base URL, and API key**,
  transparently.

### The interface is "named native agents"

All three harnesses share one shape: a spawn tool takes a **named agent/role**,
and the role definition carries a **model string** that reaches the wire. So the
parent's "which subagent?" decision *is* the routing decision.

```
parent (Fable/Astra) ── "spawn a worker for this" ──► harness spawn tool
        chooses role by name                    Task / spawn_agent / task
                                                     │
                                    role def carries model = "vector/worker"
                                                     │
                              request hits Vector with model="vector/worker"
                                                     │
                        Vector resolves {provider, base_url, api_key, real_model, shape}
                                                     │
                                       OpenRouter / Baseten / Z.ai / …
```

Vector therefore adds **role definitions + a resolver**, not an orchestration
surface. The parent keeps using its native spawn tool; the work never leaves the
harness.

### Installed role catalog (small on purpose)

Semantic, model-agnostic names. The real model behind each is Vector config, so
swapping GLM‑5.3‑Flash → DeepSeek‑V4‑Flash never touches the harness.

| Agent/role | Use when | Default backend (via registry) |
|---|---|---|
| `vector-scout` | read-only search, read, summarize | GLM‑5.3‑Flash / DeepSeek‑V4‑Flash |
| `vector-worker` | scoped edits, resolve comments, mechanical fixes | GLM‑5.3‑Flash |
| `vector-reviewer` | code review, test critique, PR review | Kimi‑K3 / GLM‑5.3 |
| `vector-researcher` | long-context doc/benchmark reading | Kimi‑K3 / Gemini‑3.8‑Flash |
| `vector-lead` | multi-step coordination on medium work | GLM‑5.3 / native frontier |
| `vector-escalate` | hard or repeated-failure tasks | native Opus / Fable / Astra |
| `vector-auto` | parent unsure; registry decides | per task class |

### Per-harness installation

- **Claude Code** — generated `.claude/agents/vector-worker.md` with
  `model: vector-worker`; `CLAUDE_CODE_SUBAGENT_MODEL=vector-scout` for unnamed
  spawns; `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1` so the gateway's
  `/v1/models` advertises the `vector-*` names (required — otherwise the CLI may
  reject unknown model IDs). Main traffic passes through to native Anthropic with
  the *inbound* credential, so the subscription is used only for the planner.
- **Codex** — `[agents.vector-worker]` + `$CODEX_HOME/agents/vector-worker.toml`
  with `model = "vector/worker"`, `model_provider = "vector"`. The **main session
  stays fully native** (ChatGPT plan); only children touch Vector. Cleanest of the
  three — no main-traffic proxying at all.
- **OpenCode** — `agent.vector-worker.model = "vector/worker"` plus a `vector`
  provider entry (`@ai-sdk/openai-compatible`, `baseURL` at the gateway; dummy
  `OPENAI_API_KEY` guard for the sub-session fallback bug).

### Giving the parent enough to decide well

- **Descriptions** — the spawn tool surfaces each role's description; Vector
  generates them from the registry ("Use for read-only repo search; cheap/fast;
  no edits").
- **Routing card** — a generated `VECTOR.md` / skill / instructions block stating
  the doctrine (scout vs worker vs reviewer vs escalate) with the current
  effective models and costs. Loaded as harness context (CLAUDE.md, AGENTS.md,
  OpenCode `instructions`).
- **Optional read-only tool** — `vector_recommend(task, constraints)` returns a
  ranked role+model. It never executes work.
- **`vector-auto`** — explicit opt-in when the parent doesn't want to choose.

### Credential / base-URL swizzling

The harness holds **one** base URL (Vector) and **one** dummy token. Vector holds
the provider registry. Per request: resolve model name → provider → inject that
provider's base URL + API key + headers → translate shape. For native passthrough,
Vector reuses the **inbound Authorization**, so the plan credential is used only
where the user intends.

---

## 1b. Model intelligence registry — "which model is right for this task"

The knowledge layer that keeps role→model mappings truthful and current. It is
**not** the decider (the parent is); it defines the defaults the parent chooses
from, and it decides only for `vector-auto`.

**Task taxonomy** (what subagents actually do): repo-search, explain, summarize,
small-edit, multi-file-edit, refactor, test-write, debug, ci-fix, code-review,
pr-comment-resolution, commit-message, doc-write, long-context-read,
tool-heavy-agentic, plan/architecture.

**Data sources** (normalized on ingest; refreshed by `vector models refresh`):
- OpenRouter `/api/v1/models` — pricing, context, modalities, supported params
- models.dev — capability/tool/reasoning metadata
- Artificial Analysis — intelligence / coding / agentic indices, speed, TTFT
- LMArena — category Elo (coding, hard prompts, instruction following)
- Benchmarks — SWE-bench Verified, Terminal-Bench 2.1/3.0, DeepSWE, Aider
  polyglot, LiveCodeBench, TAU-bench/BFCL (tool use), ∞Bench/MRCR (long context)
- Optional online refresh — fetch the latest published results ("check Google")
  for models/benchmarks not yet in the static sources
- **Local telemetry** — observed success, latency, cost-per-completed-task,
  escalation rate (this is what makes it *intelligent* rather than static)

**Score for `(model, task_class)`:**
```
quality = Σ wᵢ·benchmarkᵢ(task) + α·observed_success − λ·cost − μ·latency
```
with hard filters: context window ≥ need, tool calling if required, modality.

**Outputs**
- role → ranked backends (feeds agent descriptions and `vector/*` resolution)
- `vector models show --task code-review` for humans
- preference application: `prefer`, `avoid`, `pin`, `quality_floor`,
  `max_cost_per_task`

**User preference** supplies the final override; the registry default is only a
default, and overrides are reflected back into the agent descriptions so the
parent's mental model stays accurate.

**Feedback loop** — every routed request records (role, task class, model,
outcome, cost). Escalations and retries lower a model's observed score for that
class; successes raise it. Defaults converge on your real workload, not just
public benchmarks.

---

## 2. What we are building (and explicitly not)

**Building**
- A single local daemon that speaks all three wire shapes harnesses use:
  Anthropic Messages, OpenAI Chat Completions, and OpenAI Responses.
- A provider registry: **any** OpenAI-compatible `{base_url, api_key, models[]}`,
  plus Anthropic-native and OpenAI-native upstreams. OpenRouter is the default
  multi-model provider; Z.ai / DeepSeek / Moonshot / Baseten / Gemini are peers.
- A **role model**: architect / lead / worker / scout / reviewer / escalate.
- A **policy engine** that maps (harness, role, request signals) → concrete model.
- Per-harness **adapters** that wire each CLI and declare which model its
  subagents should ask for.
- A **budget & quota governor** whose job is to protect the paid frontier
  subscription from subagent burn.
- Telemetry: per‑role, per‑model, per‑harness cost/latency/usage.

**Not building (initially)**
- Our own agent loop / orchestration framework. The harnesses already orchestrate.
- **No external subagent runtime.** Vector never hosts, sandboxes, or monitors a
  worker, and never routes work through an MCP/agent-host. Subagents always run
  natively inside their parent harness (see §1a).
- A UI-first product. CLI + config first; a status board later.
- A model trainer, initially. Learned routing comes after telemetry exists.
- A replacement for baseten-switch. Vector is harness-agnostic and
  provider-agnostic; it can coexist (see §11).

---

## 3. How the harnesses actually work (the part that matters)

### 3.1 Claude Code (v2.1.x)

**Wire protocol:** Anthropic Messages (`POST /v1/messages`), SSE streaming.
**Redirect surface:** `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN` (or
`ANTHROPIC_API_KEY=""` explicitly empty when using a third party).

**Model selection precedence (highest → lowest)**
1. `--model` / in-session `/model`
2. `ANTHROPIC_MODEL` env
3. `model` in settings.json
4. `ANTHROPIC_DEFAULT_MODEL` (startup model)
5. built-in default

**Family aliases** the CLI understands: `fable`, `opus`, `sonnet`, `haiku`
(and `opusplan` in plan mode). Each alias resolves through:
- `ANTHROPIC_DEFAULT_FABLE_MODEL`
- `ANTHROPIC_DEFAULT_OPUS_MODEL`
- `ANTHROPIC_DEFAULT_SONNET_MODEL`
- `ANTHROPIC_DEFAULT_HAIKU_MODEL`

**Subagent model precedence (v2.1.251+):**
1. per-invocation `model` parameter (when Claude spawns an agent)
2. the agent definition's `model:` frontmatter (`opus|sonnet|haiku|fable|inherit|<full-id>`)
3. `CLAUDE_CODE_SUBAGENT_MODEL`
4. the main conversation's model

`CLAUDE_CODE_SUBAGENT_MODEL_FORCE` exists to force *all* subagents (including
built-in Explore/Plan, which otherwise ignore `CLAUDE_CODE_SUBAGENT_MODEL`).
Setting `CLAUDE_CODE_SUBAGENT_MODEL=inherit` is the same as unset.

**Other useful knobs**
- `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1` → CLI queries the gateway's
  `/v1/models` and offers those as pickable models.
- `ANTHROPIC_CUSTOM_HEADERS` → inject arbitrary headers (useful for role tagging).
- `--agents <json>`, `.claude/agents/*.md`, `--forward-subagent-text`.

**Why this is great for us:** subagents can be given a model string that only
makes sense to Vector (`vector/worker`). Model-name dispatch is therefore more
robust than trying to sniff sidechain requests. Baseten-switch does both: it sets
`CLAUDE_CODE_SUBAGENT_MODEL` *and* sniffs sidechain traffic in the gateway as a
catch-all for built-in agents and per-spawn overrides.

**Gotchas**
- An empty (not unset) `ANTHROPIC_API_KEY` is required when pointing at a third
  party, or Claude Code may fall back to Anthropic auth.
- Built-in Explore/Plan subagents need `_FORCE` (or a gateway-side classifier).
- Thinking blocks: the Anthropic Skin passes them through; a naive
  Anthropic→OpenAI translation must map `thinking` ↔ `reasoning_content`.

### 3.2 Codex CLI (0.153.4)

**Wire protocol:** OpenAI Responses API (`POST /v1/responses`) for the primary
path. `wire_api` historically also accepted `"chat"`; current Codex prefers
`"responses"`. Third‑party OpenAI‑compatible gateways often only implement
`/chat/completions`, so the router should **speak Responses down to Codex and
Chat/Responses up to providers**, translating as needed.

**Config layering:** `~/.codex/config.toml` (user) → `-p/--profile <name>` layers
`$CODEX_HOME/<name>.config.toml`. `model_provider` and `model_providers` are
honored **only** in user-level config, not project-local `.codex/config.toml`.

**Provider block**
```toml
model = "gpt-6-astra"
model_provider = "openai"
model_reasoning_effort = "medium"
plan_mode_reasoning_effort = "high"

[model_providers.vector]
base_url = "http://127.0.0.1:7331/v1"   # must include /v1
env_key = "VECTOR_API_KEY"              # name of the env var, not the key
wire_api = "responses"                  # or "chat"
requires_openai_auth = false
http_headers = { "X-Vector-Harness" = "codex" }
```

**Multi-agent** is now native:
```toml
[features]
multi_agent = true

[features.multi_agent_v2]
enabled = true
max_concurrent_threads_per_session = 30
tool_namespace = "collaboration"
hide_spawn_agent_metadata = false
```

**Custom agent roles** (the key lever):
```toml
[agents.worker]
description = "Scoped implementation worker"
config_file = "agents/worker.toml"
```
```toml
# ~/.codex/agents/worker.toml
model = "vector/worker"
model_reasoning_effort = "medium"
model_provider = "vector"
```

**The known failure mode we exist to fix:** if roles don't pin a model, every
`spawn_agent` child copies the parent's model + reasoning effort. Three subagents
on "Sol Ultra" = three simultaneous max‑effort frontier streams burning the
Codex plan. This is exactly the burn Vector prevents: define roles, pin each to a
virtual model, and let the router pick the real (cheap) backend.

**Gotchas**
- `base_url` must include `/v1`; Claude Code's does not (opposite conventions).
- `env_key` is the variable *name*.
- Project-local provider config is ignored — adapters must write user-level.
- V2 changed `spawn_agent`'s schema over time (`agent_type`/`model`/`reasoning_effort`
  were exposed, then restricted). Don't depend on per-spawn model; depend on
  **role config files**, which are stable.

### 3.3 OpenCode

**Wire protocol:** OpenAI Chat Completions via the AI SDK, plus many native
provider SDKs. Best-in-class for per-agent config.

**Provider config (any OpenAI-compatible endpoint):**
```jsonc
{
  "provider": {
    "vector": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Vector Router",
      "options": { "baseURL": "http://127.0.0.1:7331/v1", "apiKey": "local" },
      "models": {
        "worker":  { "name": "Vector Worker" },
        "lead":    { "name": "Vector Lead" },
        "scout":   { "name": "Vector Scout" },
        "escalate":{ "name": "Vector Escalate" }
      }
    }
  }
}
```

**Agents:**
```jsonc
{
  "agent": {
    "worker":  { "mode": "subagent", "model": "vector/worker",  "temperature": 0.1 },
    "reviewer":{ "mode": "subagent", "model": "vector/reviewer" },
    "planner": { "mode": "primary",  "model": "anthropic/claude-opus-5" }
  }
}
```
Subagents that don't specify a model inherit the invoking primary agent's model —
so Vector adapters should specify one explicitly on every subagent we generate.
OpenCode v2 also supports `#variant` (e.g. `vector/worker#high`) and structured
`{ providerID, model, variant }`, and `experimental.policies` can deny provider
use entirely.

**Gotcha to design around:** a known bug (#20725) makes
`@ai-sdk/openai-compatible` **sub-sessions** fall back to `@ai-sdk/openai` and
demand `OPENAI_API_KEY`. Mitigations: document a dummy `OPENAI_API_KEY`, or use a
provider id that the fallback path doesn't trip, or upstream a fix. The adapter
should verify a real subagent call end‑to‑end (`vector doctor --probe`).

### 3.4 Any other harness

The portable contract is:
1. It can point at a base URL (Anthropic or OpenAI shape), and
2. it can name a model per request (ideally per subagent).

If (1) and (2) hold, Vector works. If only (1) holds, Vector falls back to
wire-level classification (§6).

---

## 4. Prior art: what to steal from baseten-switch and CCR

**baseten-switch** (Go, launchd-supervised, menubar app)

| Feature | Take? |
|---|---|
| Front **door** + **router** split, loopback only | Yes — clean separation of "harness-facing" and "upstream" |
| `protocol_shape: anthropic \| openai` per client | Yes, extend to three shapes |
| `model_routes` family pins (fable/opus/sonnet/haiku) | Yes — map aliases to virtual models |
| `subagent_model` + `subagent_routing on/off` | Yes, generalize to a role→model policy |
| `fallback_route` + cooldown + `ttft_timeout` | Yes — core resilience |
| `model_aliases` + `/v1/models` discovery | Yes — this is how virtual models become pickable |
| Catalog-validated `reasoning` policies per harness | Yes — reasoning effort is half the cost story |
| JSONL telemetry + `spend` + retention | Yes |
| SIGHUP hot-reload, no restart | Yes |
| Backup/restore with drift detection on harness config | Yes — but *never* clobber user edits |
| "reverse translation not implemented" | **No — this is our differentiator.** Vector translates both directions. |

**claude-code-router (CCR)** — the OSS ancestor of this whole category

- `Providers[]` = `{name, api_base_url, api_key, models[], transformer}` — the
  exact "any OpenAI-compatible URL + key + models" shape the user asked for.
- `Router{default, background, think, longContext, longContextThreshold, webSearch,
  image, fallback}` — scenario routing. Vector generalizes "scenario" to "role"
  and adds a complexity classifier.
- `CUSTOM_ROUTER_PATH` — a JS hook that can override routing per request. Great
  escape hatch; Vector should expose an equivalent (a wasm/JS policy hook or a
  declarative rules DSL).
- Now supports many harnesses (Claude Code, Codex, OpenCode, Pi, …). Confirms the
  "one local endpoint, many harnesses" thesis.

---

## 5. Architecture

### 5.1 Components

```
┌─────────────── vector CLI ────────────────┐
│ up/down/status/doctor/spend               │
│ harness adapters  (claude/codex/opencode) │
│ config lint + hot reload                  │
└──────────────────┬────────────────────────┘
                   │ control (unix socket / admin port)
┌──────────────────▼────────────────────────┐
│                  core daemon               │
│  listeners: anthropic | openai-chat | openai-responses
│                                             │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  │
│  │ normalize│→ │ classify │→ │ policy   │  │
│  └──────────┘  └──────────┘  └────┬─────┘  │
│                                    ▼        │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  │
│  │ budget   │← │ route    │← │ registry │  │
│  └────┬─────┘  └────┬─────┘  └──────────┘  │
│       ▼             ▼                       │
│  ┌───────────────────────────────────────┐  │
│  │ provider adapters + translators       │  │
│  │ openai-compat | anthropic | openai    │  │
│  └───────────────────────────────────────┘  │
│  telemetry (JSONL) · rate limiters · cache  │
└─────────────────────────────────────────────┘
```

### 5.2 Request lifecycle

1. **Normalize** the inbound body to an internal `RouteRequest`
   (`harness`, `shape`, `model_requested`, `messages`, `tools`, `thinking`,
   `token_estimate`, `headers`, `stream?`).
2. **Classify role** — explicit signal first, heuristics second (§6).
3. **Resolve policy** — role + signals + budget → `RouteDecision`
   (`provider`, `model`, `reasoning_effort`, `fallbacks[]`, `cache_policy`).
4. **Govern budget** — check rate/concurrency/spend; downgrade, queue, or reject.
5. **Translate** to the provider's shape; strip/remap unsupported fields.
6. **Dispatch** with retry + fallback + cooldown on failure.
7. **Normalize the stream back** to the inbound shape (thinking ↔ reasoning).
8. **Record telemetry** — role, requested model, actual model, tokens, cost,
   latency, fallback depth, classifier verdict.

### 5.3 Internal vs external model IDs

External (what harnesses ask for):
- `vector/auto` — let the classifier decide.
- `vector/architect`, `vector/lead`, `vector/reviewer`, `vector/worker`,
  `vector/scout`, `vector/escalate` — role aliases.
- `vector/<provider>/<model>` — pin a concrete backend (escape hatch).
- Native IDs (`claude-opus-5`, `gpt-6-astra`, …) — passthrough to the native plan.

Internal: `{provider, model_id}` resolved through the registry.

---

## 6. Identifying subagent traffic (the crux)

Layered, in order of preference. Every layer is observable in telemetry so we can
see which one fired.

**L1 — Explicit per-agent model (preferred, most robust).**
Adapters give subagents a distinct virtual model (`vector/worker`). The request's
`model` field *is* the role. No inference needed.
- Claude Code: `CLAUDE_CODE_SUBAGENT_MODEL=vector/worker` + generated
  `.claude/agents/*.md` with `model: vector/worker`.
- Codex: `[agents.<role>]` config files pinning `model = "vector/<role>"`.
- OpenCode: `agent.<name>.model = "vector/<role>"`.

**L2 — Header tagging (portable, cheap).**
Where the harness allows custom headers, inject identity:
- Claude Code: `ANTHROPIC_CUSTOM_HEADERS="X-Vector-Role: worker"` (per-process) or
  via a wrapper.
- Codex: `http_headers` on the provider block.
- OpenCode: provider `options.headers`.
Use `X-Vector-Harness` and `X-Vector-Role` (optional `X-Vector-Task-Class`).

**L3 — Wire classification (catch-all).**
For traffic that doesn't self-identify (Claude Code built-in Explore/Plan,
per-spawn model picks, background tasks; Codex children that copy the parent):
- **Claude Code sidechain signal:** subagent requests carry a distinct system
  prompt and no top-level user turn; also detectable via request metadata and the
  known `claude-*` family + content shape. Baseten-switch calls this
  `subagent_routing` and tracks it as `subagent_traffic`.
- **Codex Responses signal:** presence of the multi-agent tool namespace
  (`spawn_agent` in `tools`/`tool_choice`), the `x-codex-*` headers, and parent
  vs child thread ids (`hide_spawn_agent_metadata=false` exposes them).
- Classifier maintains a per-harness rule set, versioned, with a fallback
  "unknown → treat as primary" default so we never route a planner to a weak model.

**L4 — Harness hook/plugin (deepest integration).**
- Claude Code hooks, OpenCode plugins, Codex hooks (`[features] hooks=true`) can
  stamp requests or call `vector` for a routing decision before spawning.
- Reserved for later; L1–L3 cover the initial scope.

> **Design rule:** never let a classifier *downgrade* a request it isn't sure
> about. Mis-routing the architect is catastrophic; mis-routing one worker is a
> cheap retry.

---

## 7. Intelligent routing

> **Who decides:** the parent frontier model decides task complexity and which
> subagent to use (§1a). Vector's classifier is **not** the primary
> decision-maker — it is (a) the resolver for `vector/auto`, (b) the catch-all for
> traffic that cannot self-identify (§6 L3), and (c) the escalation/cascade safety
> net. See §1b for the registry that supplies the defaults.

### 7.1 The role model

| Role | Purpose | Default tier | Typical pool |
|---|---|---|---|
| `architect` | understand issue, plan, break down | frontier (native) | Claude Fable/Opus, GPT‑6 Astra (Claude Code/Codex plan) |
| `lead` | coordinate, split work, adjudicate | frontier or smart | native frontier; GLM‑5.3 for medium |
| `reviewer` | code review, test critique, PR comments | smart | Kimi‑K3, GLM‑5.3, DeepSeek‑V4‑Pro |
| `worker` | implement scoped tasks, resolve comments | cheap | GLM‑5.3‑Flash, DeepSeek‑V4‑Flash |
| `scout` | search, read, summarize | cheapest | GLM‑5.3‑Flash, DeepSeek‑V4‑Flash |
| `escalate` | hard sub-task, repeated failure | frontier | native frontier models |

This directly encodes the user's intent: planning/understanding/breakdown on
Claude + Codex; monotonous PR/comment/search work on GLM/DeepSeek/Kimi; frontier
(Opus) pulled in only for complexity.

### 7.2 Complexity classifier

Start **deterministic + cheap**, evolve to **learned**:

**Signals**
- explicit role / agent name
- requested reasoning effort / thinking budget
- tool surface: read-only vs `edit`/`write`/`bash`
- estimated input tokens (long-context → Kimi‑K3 or Gemini‑Flash tier)
- task verbs in the first user/task text (`review`, `rename`, `summarize` vs
  `design`, `debug`, `architect`, `migrate`)
- presence of failing-test / stack-trace / diff payloads
- attempt count (retry ⇒ escalate one tier)
- historical success rate for that (role, task-shape) on each model

**Decision**
- `score` → tier. Cheap by default for `scout`/`worker`; smart for `reviewer`;
  frontend tier for `architect`/`escalate`.
- Ambiguous cases: optionally a *one-token* classification call to a very cheap
  model (GLM‑5.3‑Flash), cached by prompt hash. Keep it off the critical path
  (dispatch cheap immediately, re-route on the next turn if needed).

**Cascade / escalation (the "allow Opus in")**
```
worker task → GLM-5.3-Flash
   ├─ tool error / refusal / loop / test still failing?
   │      → retry on GLM-5.3 (smart worker)
   │      → then DeepSeek-V4-Pro / Kimi-K3 (reviewer tier)
   └─ still stuck OR explicit `vector/escalate` OR labeled high-complexity
          → native frontier (Opus / Fable / Astra)
```
Record every escalation; learn which task shapes need which floor.

**Explicit escalation paths**
- Harness asks for `vector/escalate`.
- Agent calls a Vector MCP/tool `escalate(reason, context)`.
- Policy rule: e.g. `if role==worker and task matches /migration|race condition|security/ → architect tier`.
- Config-level: `complexity_threshold` per role.

### 7.3 Selection within a tier

Pick the **cheapest model that satisfies the capability tags** the request needs
(`tools`, `long_context`, `vision`, `strict_json`), tie-broken by health/latency.
This is where the provider registry + capability metadata (models.dev / OpenRouter
models API) pays off.

---

## 8. Quota & subscription protection

This is a first-class goal, not an afterthought.

- **Plan isolation.** Native-plan credentials (Claude subscription, ChatGPT/Codex
  plan OAuth) are only reachable via the `primary`/`architect` route. Subagent
  routes can *never* resolve to a plan credential unless explicitly escalated.
- **Per-provider limiters.** RPM, TPM, max concurrency, and burst. E.g. DeepSeek
  V4 Flash tolerates very high concurrency; smaller providers don't. Respect
  `Retry-After`; back off; cooldown on failure (baseten-switch: 30s).
- **Budget caps.** Daily/weekly USD per harness, per role, per provider. On
  breach: downgrade tier → queue → hard stop (configurable).
- **Fallback chains.** Ordered: cheap peer → smart peer → native (only if allowed).
  Never silently explode cost.
- **Cost nudges.** Prefer prompt-caching-friendly providers (DeepSeek cached input
  ~98% off; GLM cached input cheap), short system prompts per role, and stable
  prefixes so cache hits land.
- **Circuit breakers + `ttft_timeout`.** If a cheap provider stalls, fall through
  to the next rather than hanging the subagent.
- **Concurrency governance.** Cap simultaneous subagents per harness so one
  fan-out doesn't exhaust a pool.

---

## 9. Provider abstraction

The user's requirement — "configure OpenRouter from any OpenAI-compatible URL,
key, and models" — generalizes to a provider registry.

```yaml
providers:
  - id: openrouter
    type: openai_compatible
    base_url: https://openrouter.ai/api/v1
    api_key: ${OPENROUTER_API_KEY}
    # OpenRouter also exposes an Anthropic skin at the same host; set
    # anthropic_base_url when a client shape needs it.
    anthropic_base_url: https://openrouter.ai/api/v1
    headers:
      HTTP-Referer: https://github.com/anunay999/vector
      X-Title: vector

  - id: zai
    type: openai_compatible
    base_url: https://api.z.ai/api/paas/v4
    api_key: ${ZAI_API_KEY}

  - id: deepseek
    type: openai_compatible
    base_url: https://api.deepseek.com/v1
    api_key: ${DEEPSEEK_API_KEY}

  - id: moonshot
    type: openai_compatible
    base_url: https://api.moonshot.ai/v1
    api_key: ${MOONSHOT_API_KEY}

  - id: anthropic-native
    type: anthropic
    base_url: https://api.anthropic.com
    auth: subscription | ${ANTHROPIC_API_KEY}

  - id: openai-native
    type: openai_responses
    base_url: https://api.openai.com/v1
    auth: subscription | ${OPENAI_API_KEY}
```

**Translation matrix** (the hard, valuable part): inbound shape × upstream type.
Vector must map:
- `system` handling (Anthropic top-level vs OpenAI messages)
- `tools` / `tool_choice` / `tool_result` ↔ `function`/`tool_calls`
- `thinking` ↔ `reasoning_content` / `reasoning` + `reasoning_effort`
- SSE event names and deltas
- token counting/usage field names
- `max_tokens`, stop sequences, streaming semantics

Baseten-switch declines reverse translation; Vector doing it is what makes
"any provider behind any harness" true.

---

## 10. Config sketch (single source of truth)

```yaml
version: 1
listen:
  anthropic: 127.0.0.1:7331
  openai:    127.0.0.1:7331
routing_enabled: true

models:            # capability + price registry (can be auto-filled from provider)
  - id: z-ai/glm-5.3-flash
    tags: [cheap, fast, tools, long_context]
    context: 1_310_720
    price: { in: 0.15, out: 0.50 }
  - id: deepseek/deepseek-v4-flash
    tags: [cheap, fast, tools, high_throughput]
    price: { in: 0.14, out: 0.28 }
  - id: moonshotai/kimi-k3
    tags: [smart, tools, long_context, agentic]
    context: 1_048_576
    price: { in: 2.50, out: 14.00 }
  - id: z-ai/glm-5.3
    tags: [smart, reasoning, code]
    price: { in: 1.007, out: 3.41 }

roles:
  architect: { tier: frontier, prefer: [native/anthropic, native/openai] }
  lead:      { tier: frontier, prefer: [native/anthropic, openrouter/z-ai/glm-5.3] }
  reviewer:  { tier: smart,    prefer: [openrouter/moonshotai/kimi-k3, openrouter/z-ai/glm-5.3] }
  worker:    { tier: cheap,    prefer: [openrouter/z-ai/glm-5.3-flash, openrouter/deepseek/deepseek-v4-flash] }
  scout:     { tier: cheap,    prefer: [openrouter/deepseek/deepseek-v4-flash, openrouter/z-ai/glm-5.3-flash] }
  escalate:  { tier: frontier, prefer: [native/anthropic, native/openai] }

policies:
  - match: { harness: claude-code, role: primary }
    route: architect          # pass native, preserve quality
  - match: { harness: claude-code, role: subagent }
    route: worker
  - match: { harness: codex, role: primary }
    route: architect
  - match: { harness: codex, role: subagent }
    route: worker
  - match: { role: reviewer }
    route: reviewer
  - match: { complexity: high }
    route: escalate

complexity:
  default_floor: worker
  signals: [thinking_budget, tool_surface, tokens, task_verbs, retries]
  cheap_classifier: openrouter/z-ai/glm-5.3-flash
  escalate_on: [tool_error, no_progress, explicit, high_complexity]

budget:
  daily_usd: 25
  per_provider: { openrouter: 20, deepseek: 5 }
  on_breach: downgrade   # downgrade | queue | stop
  max_concurrent_subagents_per_harness: 8

fallback:
  cooldown: 30s
  ttft_timeout: 30s
  chain: [worker, reviewer, escalate]

harnesses:
  claude-code: { enabled: true, subagent_model: vector/worker }
  codex:       { enabled: true, profile: vector }
  opencode:    { enabled: true }

telemetry:
  dir: ~/.config/vector/telemetry
  retention_days: 90
```

Provider IDs are namespaced (`openrouter/…`) so a model can exist on several
providers; the registry picks by price/health.

---

## 11. Harness adapters (what Vector writes)

Each adapter is **idempotent, backed up, drift-aware, reversible** (borrow
baseten-switch's backup/journal discipline).

**Claude Code**
- `~/.claude/settings.json`:
  `env.ANTHROPIC_BASE_URL`, `env.ANTHROPIC_AUTH_TOKEN`, `env.ANTHROPIC_API_KEY=""`,
  `env.CLAUDE_CODE_SUBAGENT_MODEL`, optional `env.CLAUDE_CODE_SUBAGENT_MODEL_FORCE`,
  `env.CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1`,
  `env.ANTHROPIC_CUSTOM_HEADERS`.
- optional generated `.claude/agents/*.md` with `model: vector/<role>`.
- `vector claude on|off|status`.

**Codex**
- managed overlay `$CODEX_HOME/vector.config.toml` (never edits user
  `config.toml`); user runs `codex --profile vector`.
- `[model_providers.vector]` + `[agents.<role>]` role files under
  `$CODEX_HOME/agents/`.
- `vector codex on|off|status`.

**OpenCode**
- patch `opencode.json`: add `provider.vector` (`@ai-sdk/openai-compatible`,
  `baseURL`, `models`) and `agent.<role>.model = "vector/<role>"`.
- dummy `OPENAI_API_KEY` guard for the sub-session fallback bug.
- `vector opencode on|off|status`.

**Verification:** `vector doctor --probe` sends a 1-token request through each
configured harness path and asserts the routed model, like baseten-switch's probe.

---

## 12. Suggested initial model pool

Cheap intelligence, matched to task shape (prices are OpenRouter list, per M tok):

| Model | In / Out | Best at | Use as |
|---|---|---|---|
| `z-ai/glm-5.3-flash` | $0.15 / $0.50 | agentic, code, huge context (1.31M) | default worker/scout |
| `deepseek/deepseek-v4-flash` | $0.14 / $0.28 | throughput, classification, rote | scout, high-fanout, classifier |
| `moonshotai/kimi-k3` | $2.50 / $14 | long context, tool use, agentic | reviewer, long-context worker |
| `z-ai/glm-5.3` | $1.01 / $3.41 | reasoning, code generation | smart worker, medium lead |
| `deepseek/deepseek-v4-pro` | $0.66 / $1.98 | reasoning/list price | reviewer fallback |
| `meta/muse-spark-1.3` | $0.10 / $0.20 | long-running agentic/multi-agent | scout alternative |
| `google/gemini-3.8-flash` | $0.75 / $3.75 | multimodal, fast frontier-ish | long-context + vision worker |

Frontier (plan/native, escalation): Claude Fable 5.1 / Opus 5, GPT‑6 Astra /
Sol / Terra. Default policy: **subagents never touch these**; only `architect`,
`lead`, and explicit `escalate`.

---

## 13. MVP plan

**Phase 0 — spine (week 1)**
- **Spike first:** confirm Claude Code subscription OAuth survives the proxy
  passthrough (§0). If not, design the planner credential story before building.
- Daemon: `/v1/messages` + `/v1/chat/completions`, single provider
  (OpenRouter), static role routing, passthrough for native Anthropic.
- Config load/lint, `vector up/down/status`.
- Claude Code adapter (role agents + `CLAUDE_CODE_SUBAGENT_MODEL`).
- Codex adapter: main session stays native; child role files point at Vector.
- Success: Claude Code subagents land on GLM‑5.3‑Flash; planner stays on Opus;
  Codex plan quota is untouched by spawned agents.

**Phase 1 — multi-harness (week 2–3)**
- Add `/v1/responses` (Codex) and the Responses↔Chat translation.
- Codex adapter with role files; OpenCode adapter with provider + agents.
- `/v1/models` discovery exposing `vector/*`.
- `vector doctor --probe`.

**Phase 2 — routing brain (week 3–5)**
- Complexity classifier, cascades, explicit escalation.
- Budget governor, limiters, fallback chains, cooldowns.
- Telemetry + `vector spend`.

**Phase 3 — polish / differentiators**
- Full translation matrix (thinking/reasoning, tools, SSE) across shapes.
- Learned routing from telemetry; per-task-class floors.
- Escalation MCP/tool; policy hook (`CUSTOM_ROUTER_PATH` equivalent).
- Menubar/status board; Homebrew tap.

---

## 14. Open decisions

1. **Language:** Go (matches baseten-switch; single static binary; launchd) vs
   Rust vs TypeScript/Bun (CCR/OpenCode ecosystem). Recommendation: **Go**.
2. **Relationship to baseten-switch:** coexist/managed overlay, or standalone
   competitor? Recommendation: **standalone, harness-agnostic**, designed to be
   layerable with a Baseten provider entry.
3. **Scope of "intelligent":** deterministic policy + cheap classifier first, or
   invest in a learned/complexity model immediately? Recommendation: **start
   deterministic**, capture telemetry, then learn.
4. **Escalation trigger:** explicit only, or automatic on failure/complexity?
   Recommendation: **both** (auto cascade + explicit `vector/escalate`).
5. **Tier assumption:** confirm that Fable/Astra/Opus are the
   architect/lead/escalate tier and GLM/DeepSeek/Kimi are the worker/reviewer
   tier (with GLM‑5.3/Kimi‑K3 eligible as medium leads).

---

## 15. Resolved decisions (2026-09-10)

- **Language:** Go. Single static binary, launchd supervision, matches the
  reference.
- **Positioning:** standalone and provider-agnostic. OpenRouter first, plus
  Baseten, plus arbitrary `{base_url, api_key, models}` OpenAI-compatible
  providers. Coexists with baseten-switch rather than replacing it.
- **First slice:** Claude Code **and** Codex, wired against OpenRouter + Baseten +
  any OpenAI-compatible provider.
- **Delegation model:** parent decides; subagents run natively; Vector only
  installs role definitions and resolves model → provider/base_url/key.
- **Tiers:** confirmed. Frontier = architect/lead/escalate; cheap = worker/reviewer;
  GLM‑5.3 / Kimi‑K3 eligible as medium leads.
- **Intelligence:** add a benchmark-driven model-intelligence registry (§1b) with
  user preference overrides; the parent makes the final choice, the registry keeps
  the defaults honest and exposes the "which model for which task" knowledge.
