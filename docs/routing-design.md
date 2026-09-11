# Routing design: two modes

**Status:** current design (v0.3). Implementation-ready; no code in this document.
**Supersedes:** the routing sections of `docs/architecture-proposal.md` (§5–§8, §10)
and the older precedence lists in `AGENTS.md`, `README.md`, and
`docs/configuration.md`. The precedence statement now lives in §2 of this file.

---

## 1. The model

Vector routes in exactly two modes, and nothing else.

```
inbound request
   │
   ├─ model_map rule matches the model? ──► MODEL MODE (explicit)
   │                                          target role | model | provider
   │
   ├─ names a vector-<role> / X-Vector-Role? ─┐
   │                                           ├─ AGENT MODE
   ├─ detected subagent && subagents.route? ──┘   role's `prefer` list
   │
   └─ otherwise ──────────────────────────────► subscription passthrough
```

- **Model mode — explicit.** `model_map` forces a named inbound model (for
  example `claude-haiku*`) to a role, a registry model, or a provider. This is
  how main-session Claude/Codex models get routed, and it is the only way the
  main session leaves the subscription provider.
- **Agent mode — automatic.** A request that names `vector-<role>` (or sends
  `X-Vector-Role`) resolves through that role's `prefer` list. A structurally
  detected subagent with no explicit role resolves through the worker role while
  `subagents.route` is on (the default).

Everything else is native passthrough: the requested model is forwarded to the
subscription provider for the wire shape, unchanged.

## 2. Precedence (stated once)

| # | Step | Applies to | Result |
|---|---|---|---|
| 0 | `routing_enabled: false` | all | subscription passthrough for the shape |
| 1 | explicit registry model id (`openrouter/z-ai/glm-5.3-flash`) | all | that model, honored as asked |
| 2 | virtual role (`vector-<role>`) or `X-Vector-Role` | all | that role's `prefer` list |
| 3 | `model_map` rule whose `from` matches the requested model | all | the rule's target |
| 4 | detected subagent and `subagents.route` (default on) | subagents | the worker role |
| 5 | otherwise | all | subscription passthrough |

## 3. Config surface

**Kept (the two modes and their plumbing):**

| Surface | Purpose |
|---|---|
| `providers[]`, `models[]` | upstreams and the model registry |
| `roles` | agent mode: ordered `prefer` list per role, plus `tier`/`primary`/`description` |
| `model_map[]` | model mode: `{from, to}` redirect table |
| `subagents.route` | the one agent-mode switch: route detected subagents to the worker |
| `budget.*` | spend ceilings and concurrency |
| `fallback.ttft_timeout` | first-byte timeout |
| `harnesses.*`, `telemetry.*`, `listen`, `routing_enabled` | wiring |

**Removed (the rest):**

| Removed | Why | Replaced by |
|---|---|---|
| `policies[]` (+ `Policy`/`Match`) | an overlapping fourth routing layer | steps 2–4 above |
| `complexity.*` | no classifier exists; only `default_floor` was read | — |
| `vector-auto` | an alias advertised as a classifier | — |
| `fallback.chain` | a global cross-role chain that silently moved traffic | none: failures surface |
| `fallback.cooldown` | never read | — |
| `harnesses.*.force_subagent_model` | forcing every subagent to one model defeats agent mode | `subagents.route` |
| `router.Input.{SubagentSignal, PromptChars, ToolCount, ThinkingBudget}` | never set or read | — |
| `Decision.Candidates`, `attachFallbacks` | runtime fallback machinery | none: failures surface |

## 4. No fallback

A routed request gets a single attempt. There is no candidate list, no
cross-role chain, and no automatic move of main-session traffic. If the upstream
returns `4xx`/`5xx`/transport error, the status and body are recorded and
returned to the harness, which owns retries.

Consequences, by design:

- A main-session `429`/`5xx` is surfaced, not answered by a cheap model.
- An explicit model request is honored or fails; it is never silently swapped.
- A detected subagent that the worker model rejects fails visibly.

The pre-flight normalizer is retained and covers Claude Code's tool search as
well as `context_management`: for a non-Anthropic upstream, `defer_loading` is
removed from tools, the server-side `tool_search_tool_*` tool is dropped,
`tool_reference` blocks become plain text naming the tool,
`server_tool_use` / `tool_search_tool_result` history blocks are dropped, and
the tool-search betas are filtered out of `anthropic-beta`. Anthropic-bound
requests are forwarded byte-for-byte. See
[claude-code-context](claude-code-context.md).

## 5. Subagent detection

Header-first and scoped; it never inspects user or assistant content, so
telemetry stays metadata-only.

1. `X-Claude-Code-Agent-Id` present → subagent.
2. Codex `x-codex-turn-metadata` parses as JSON and names an `agent_name` other
   than `/root` → subagent.
3. Claude Code's billing block: the top-level `system` field (string or array of
   text blocks) contains `cc_is_subagent=true` → subagent. `messages` is never
   scanned.

Rule 3 is the regression fix for the 2026-09-11 incident: a whole-body scan
matched the literal inside a tool result when the session read vector's own
source, misclassifying the main session as a subagent.

## 6. Observability

- Every response carries `X-Vector-Request-Id`; `vector explain <id>` reconstructs
  the decision.
- When the served model differs from the requested one, the response carries
  `X-Vector-Served-By: <provider>/<model>; reason=<reason>`.
- Failed attempts record the upstream error body (truncated), not just a status.
- Telemetry keeps `class` (main/subagent), provider, role, status, latency, and
  cost per request.

## 7. Migration from v0.2

Config `version: 1` still loads; the removed keys are ignored.

| v0.2 | v0.3 |
|---|---|
| `policies: [{match: {traffic: subagent}, route: worker}]` | the built-in agent mode (no rule needed) |
| `policies: [{match: {model: X}, route: Y}]` | `model_map: [{from: X, to: Y}]` |
| `policies: [{match: {traffic: primary}, route: architect}]` | native passthrough (built in) |
| `complexity.*`, `vector-auto`, `fallback.cooldown` | dropped |
| `fallback.chain` | dropped; failing requests surface |
| `harnesses.*.force_subagent_model` | dropped; use `subagents.route` |

The `vector-*` agent names are unchanged. `vector models map` manages model mode;
`vector config set subagents.route false` turns agent mode off.

## 8. Test plan

- Body containing `cc_is_subagent=true` inside `messages` but not `system`, with
  no agent-id header → `class: main`.
- Codex `x-codex-turn-metadata` with a space after the colon is parsed.
- `subagents.route: false` → a detected subagent is subscription passthrough.
- Main-session `400`/`500` → exactly one upstream attempt; the status surfaces.
- A detected subagent that errors → exactly one attempt; the status surfaces.
- `claude on` writes `CLAUDE_CODE_SUBAGENT_MODEL` and never `_FORCE`.
