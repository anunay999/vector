# Claude Code behind the gateway: context, tool schemas, and thrash

This page records what changes for a Claude Code session when its
`ANTHROPIC_BASE_URL` is the vector gateway rather than `api.anthropic.com`, what
vector does about it, and what is left for the operator. It comes from a
post-mortem of a session that hit Claude Code's own breaker:

> Autocompact is thrashing: the context refilled to the limit within 3 turns of
> the previous compact, 3 times in a row.

## What Claude Code does differently behind a gateway

Claude Code (2.1.x) checks whether `ANTHROPIC_BASE_URL` is unset or points at
`api.anthropic.com`. When it does not, three things change:

1. **Deferred tool loading is switched off.** On a first-party URL, MCP tool
   schemas are sent flagged `defer_loading` and fetched on demand through a
   `ToolSearch` tool. Behind a gateway the CLI logs
   `ToolSearch disabled: ANTHROPIC_BASE_URL is not a first-party Anthropic host`
   and sends every tool schema in full on every request. With a dozen MCP
   servers that is 200k+ tokens of fixed payload per turn.
2. **The account is not treated as 1M-entitled.** `opus[1m]` and `sonnet[1m]`
   aliases can be refused or dropped to the standard model, and for
   `claude-opus-5` (also `claude-opus-4-6`, `claude-opus-4-8`,
   `claude-sonnet-4-6`) the auto-compact window is then hard-set to 200k.
3. **Gateway model discovery** (`CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1`,
   which `vector claude on` sets) makes the CLI read `/v1/models` from the
   gateway.

## Why that thrashes

Claude Code's auto-compact fires at roughly:

```
threshold = (window − 20k output reserve) × (1 − 0.15)
```

| Window as the CLI sees it | Threshold |
|---|---|
| 1,000,000 (`claude-opus-5[1m]`) | ~833k |
| 200,000 (`claude-opus-5`) | ~153k |

Tool schemas cost about 3.3 characters per token. A 790 KB tool inventory is
~240k tokens before a single message. Once the session is on the 200k window,
the fixed payload alone is above the threshold: every compaction is followed by
another one within a turn, each rebuilding a 200k–290k prompt from a cold cache
at full input price, until the CLI trips after three.

Vector is not what causes the loop, but a request routed to a cheap model carries
the same 240k of schemas, so the payload is also most of what a routed subagent
turn costs.

## What vector does

- **`vector claude on` sets `ENABLE_TOOL_SEARCH=auto`.** The CLI's own message
  says to set it when the proxy forwards `tool_reference` blocks. Vector
  forwards Anthropic-bound requests byte-for-byte, so deferral works there. A
  value the user already set is respected, and `vector claude off` removes only
  the value vector wrote.
- **Non-Anthropic upstreams get a tool-search-free body.** For a routed
  subagent request the gateway removes `defer_loading` from every tool (the
  cheap model gets the full list, as it always did), drops the server-side
  `tool_search_tool_*` tool, rewrites `tool_reference` blocks to plain text
  naming the tool, drops `server_tool_use` / `tool_search_tool_result` history
  blocks, and filters the tool-search betas out of `anthropic-beta`. Untouched
  fields are forwarded unchanged.
- **`/v1/models` lists the native providers' default models** (for example
  `claude-opus-5` owned by `anthropic-native`), so a harness that discovers
  models through the gateway sees its frontier model rather than a gateway that
  serves no Claude at all.
- **A per-session thrash breaker** (`guard.thrash`, see
  [configuration](configuration.md#guard)) watches for repeated cold rebuilds
  of a very large prompt and warns or blocks the session with an explanation.
- **`vector doctor`** warns when `ENABLE_TOOL_SEARCH` is unset for a wired
  Claude Code, and when the configured model carries `[1m]`.

## What is left for the operator

Vector cannot make the CLI treat a gateway as first-party. Keep the fixed
payload under the 200k-window threshold, or keep the window at 1M:

- Trim MCP servers that are not needed in every session. Prefer project-scoped
  `.mcp.json` entries over global `mcpServers`, and do not pre-approve them
  globally through `enabledMcpjsonServers` in `~/.claude/settings.json` unless
  you want them everywhere. `claude mcp list` from a checkout shows what a
  session there will load.
- Turn off tool sets you do not use: `claudeInChromeDefaultEnabled: false` in
  `~/.claude.json` for the Chrome tools, `disableClaudeAiConnectors: true` in
  settings for the claude.ai connectors.
- If you rely on the 1M window, pin the auto-compact window explicitly with
  `CLAUDE_CODE_AUTO_COMPACT_WINDOW=1000000` in the settings `env` block, and
  verify with a throwaway session that the model keeps its `[1m]` suffix after
  a `/compact`. The CLI's setting is capped by the model's window, so this only
  helps while the model is still the 1M variant.
- Watch `vector top`: the efficiency line shows the tool-search share and any
  `guard` events, and the sessions table shows each session's `cold-min`, the
  smallest prompt it ever rebuilt from a cold cache. That number is the fixed
  payload; it should fall from ~250k to well under 100k once deferral is on.
  `vector telemetry` has the raw `tool_search`, `cache_write_tokens`, and
  `guard` fields.
