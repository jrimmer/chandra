# Configuration Reference

Chandra is configured via a TOML file at `~/.config/chandra/config.toml` (permissions enforced at `0600`).
Sensitive values (API keys, tokens) belong in `~/.config/chandra/secrets.toml` (`0600`) — Chandra reads both
and merges them at startup. The daemon refuses to start if either file has loose permissions.

Environment variable interpolation is supported in all string values: `${MY_VAR}` is replaced with the
process environment. Unresolved variables are logged as warnings and left as empty strings.

---

## Quick reference

```toml
[identity]
name                = "Chandra"           # default
persona_file        = "~/.config/chandra/persona.md"
max_tool_rounds     = 10                  # default 5

[provider]
type                = "openai"            # openai | anthropic | openrouter | ollama | custom
base_url            = "https://openrouter.ai/api/v1"
api_key             = "${OPENROUTER_API_KEY}"
default_model       = "anthropic/claude-sonnet-4-6"
embedding_model     = "text-embedding-3-small"  # default

[embeddings]
# Legacy section — use provider.embedding_model instead.
base_url            = "http://localhost:11434/v1"
model               = "nomic-embed-text"
dimensions          = 768

[database]
path                = "~/.config/chandra/chandra.db"

[budget]
semantic_weight     = 0.4   # default
recency_weight      = 0.3   # default
importance_weight   = 0.3   # default
recency_decay_hours = 168   # default (1 week)

[scheduler]
tick_interval       = "60s" # default

[mqtt]
mode                = "embedded"          # embedded | external | disabled
bind                = "127.0.0.1:1883"    # embedded mode only

[channels.discord]
bot_token           = "Bot ${DISCORD_BOT_TOKEN}"
channel_ids         = ["1234567890123456789"]
access_policy       = "invite"            # default
require_mention     = true                # default
rate_limit_per_minute = 0                 # default (unlimited)
reaction_status     = true                # default
edit_in_place       = true

[tools]
confirmation_timeout = "24h"              # default
default_tool_timeout = "30s"              # default

[skills]
path                = "~/.config/chandra/skills"  # default
max_matches         = 3                   # default
max_context_tokens  = 2000                # default

[operator]
config_confirm_timeout_secs = 30          # default, clamped [15, 120]
```

---

## `[identity]` — Agent identity

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | `"Chandra"` | Display name used in logs and self-identification. |
| `description` | string | `"A helpful personal assistant"` | One-line description of the agent's purpose. |
| `persona_file` | string | `""` | Path to a Markdown file loaded as the agent's system prompt. If empty, a minimal default prompt is used. Supports `~` expansion. **Recommended:** set this and keep the file under version control alongside your config. |
| `max_tool_rounds` | int | `5` | Maximum tool-call rounds per turn before the loop is aborted. Each round is one LLM call + parallel tool execution. A value of `5` is safe for most tasks; raise to `10–15` for complex multi-step agent work. Too high risks runaway loops; too low cuts off legitimate reasoning. |

**Recommendation:** set `persona_file` to a Markdown file that describes the agent's operating style, tool usage patterns, and parallel execution guidance. The agent reads this on every turn — it is the single most impactful config change you can make.

---

## `[provider]` — LLM provider

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `type` | string | — | **Required.** Provider type: `openai`, `anthropic`, `openrouter`, `ollama`, `custom`. Controls client behaviour (auth header format, API endpoint path, error handling). |
| `base_url` | string | — | **Required.** Base URL for the LLM API. Must use HTTPS for all non-localhost endpoints. RFC-1918 private addresses are blocked (SSRF guard). Anthropic: do not include `/v1` — the SDK appends it. |
| `api_key` | string | — | API key for the provider. Omit from `config.toml`; put in `secrets.toml` or use `${ENV_VAR}`. |
| `default_model` | string | `"gpt-4o"` (openai) | **Required.** Model ID to use for all turns unless overridden per-conversation with `!model`. |
| `embedding_model` | string | `"text-embedding-3-small"` | Embedding model ID. Used for semantic memory storage and retrieval. See `[embeddings]` for the local Ollama alternative. |

**Provider examples:**
```toml
# OpenRouter (recommended — model-agnostic, easy to switch)
[provider]
type          = "openrouter"
base_url      = "https://openrouter.ai/api/v1"
default_model = "anthropic/claude-sonnet-4-6"

# Anthropic direct
[provider]
type          = "anthropic"
base_url      = "https://api.anthropic.com"   # no /v1 — SDK adds it
default_model = "claude-sonnet-4-6"

# Local Ollama (no API key needed)
[provider]
type          = "ollama"
base_url      = "http://localhost:11434/v1"
default_model = "llama3.1"
```

**Security:** `base_url` validation runs at startup. HTTP (non-TLS) is rejected for all non-localhost endpoints. Private IP ranges are blocked to prevent SSRF via the config file.

---

## `[embeddings]` — Local embedding provider *(legacy)*

> **Note:** This section is kept for backwards compatibility. New configs should set `provider.embedding_model` instead. If both are set, `provider.embedding_model` takes precedence for the model ID; `embeddings.base_url` is still used as the embedding endpoint when it differs from `provider.base_url`.

Typical setup: chat LLM via OpenRouter (cloud), embeddings via local Ollama (private).

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `base_url` | string | `""` | Embedding API endpoint. Use `http://localhost:11434/v1` for Ollama. |
| `api_key` | string | `""` | API key for the embedding endpoint. Empty for local Ollama. |
| `model` | string | `""` | Embedding model ID. `nomic-embed-text` (768 dims) is recommended for local use; `text-embedding-3-small` for OpenAI. |
| `dimensions` | int | `1536` | Embedding vector dimensions. Must match the model. `nomic-embed-text` → `768`; `text-embedding-3-small` → `1536`. Mismatch causes silent retrieval degradation. |

**Recommendation:** use local Ollama for embeddings. Conversation content never leaves your machine for the embedding step, which is the highest-volume private data in a memory-enabled agent.

```toml
[embeddings]
base_url   = "http://localhost:11434/v1"
model      = "nomic-embed-text"
dimensions = 768
```

---

## `[database]` — Storage

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `path` | string | — | **Required.** Path to the SQLite database file. Supports `~` expansion. The directory is created if it doesn't exist. |

All agent state — episodic memory, semantic memory, intents, sessions, access control, token usage, action log — lives in this single file. WAL mode is enabled at open time. Regular backups are strongly recommended.

```toml
[database]
path = "~/.config/chandra/chandra.db"
```

---

## `[budget]` — Context Budget Manager

The CBM scores and ranks memory candidates before each LLM call, then assembles a context window
within the model's token limit. Scores determine what gets included when space is tight.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `semantic_weight` | float | `0.4` | Weight of semantic similarity score (vector + BM25 RRF) in the candidate ranking. |
| `recency_weight` | float | `0.3` | Weight of recency score. Recent memories are favoured; decay is exponential. |
| `importance_weight` | float | `0.3` | Weight of importance score. Memories stored with a high importance value (e.g. "remember" reinforcement) rank higher. |
| `recency_decay_hours` | int | `168` | Half-life for recency decay in hours. Default 168h = 1 week: a memory from 1 week ago scores 0.5 of a fresh memory. |

The three weights are used as a weighted sum: `score = semantic*sw + recency*rw + importance*iw`. They do not need to sum to 1.0, but keeping them in the same order of magnitude gives predictable behaviour.

**Tuning guidance:**
- Increase `semantic_weight` if the agent is missing contextually relevant but older memories
- Increase `recency_weight` if recent context is being crowded out by older semantic matches
- Decrease `recency_decay_hours` (e.g. `48`) for short-lived workloads; increase (e.g. `720`) for long-term relationship memory

---

## `[scheduler]` — Turn scheduler

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `tick_interval` | duration string | `"60s"` | How often the scheduler checks for due intents. Go duration format: `"30s"`, `"2m"`, `"1h"`. Lower values make scheduled turns more punctual at the cost of more DB queries. `"60s"` is a good default. |

The scheduler uses SQLite queries, not goroutine timers, so it is restart-safe. On startup, `RecoverMissedJobs` reschedules any overdue recurring intents with a 5-second stagger to avoid startup bursts.

---

## `[mqtt]` — MQTT event bus

MQTT enables external events (device state, webhooks, monitoring alerts) to trigger agent reasoning without a user message. Optional — set `mode = "disabled"` if not needed.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `mode` | string | `"embedded"` | `embedded`: run a lightweight broker in-process. `external`: connect to an existing broker. `disabled`: no MQTT. |
| `bind` | string | `"127.0.0.1:1883"` | Listen address for embedded mode. Change to `0.0.0.0:1883` for external access (add firewall rules). |
| `broker` | string | `""` | Broker URL for external mode: `tcp://host:1883`, `ssl://host:8883`. |
| `topics` | string[] | `[]` | MQTT topics to subscribe to. Supports wildcards: `homelab/#`, `sensors/+/temperature`. |
| `username` | string | `""` | MQTT auth username. For external brokers. |
| `password` | string | `""` | MQTT auth password. Use `secrets.toml` or `${ENV_VAR}`. |
| `tls_cert` | string | `""` | Path to TLS client certificate (PEM). |
| `tls_key` | string | `""` | Path to TLS client key (PEM). |

---

## `[channels.discord]` — Discord channel

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Explicit enable/disable. Rarely needed — omitting the section disables Discord. |
| `bot_token` | string | — | **Required.** Discord bot token with `MESSAGE CONTENT` privileged intent enabled. Format: `Bot MTQ...`. Store in `secrets.toml`. |
| `channel_ids` | string[] | — | **Required.** Discord channel IDs the bot listens to. Messages from other channels are silently dropped. |
| `access_policy` | string | `"invite"` | Access control mode: `invite` (require invite code), `allowlist` (explicit user list), `open` (anyone in the channel). `open` is strongly discouraged in production. |
| `require_mention` | bool | `false`* | When `true`, the bot only responds when directly `@mentioned` or when replying to a bot message. **Set to `true` for production** — `false` is for testing only and causes the bot to respond to every message in the channel. |
| `allowed_users` | string[] | `[]` | Static allowlist of Discord user IDs. Only used when `access_policy = "allowlist"`. An empty list is a **hard error** — the daemon will not start to prevent accidental lock-out. |
| `allowed_guilds` | string[] | `[]` | Restrict to specific guild IDs. |
| `allowed_roles` | string[] | `[]` | Restrict to users with specific role IDs. |
| `allow_bots` | bool | `false` | Allow messages from other bot accounts. Default `false`. Set `true` for cross-bot testing only — enabling in production allows feedback loops. |
| `rate_limit_per_minute` | int | `0` | Max messages processed per user per minute. `0` = unlimited. Recommended: `30` for multi-user deployments. Exceeded messages are silently dropped with a warning log (no reply to the user). |
| `reaction_status` | *bool | `true` | Show emoji reaction status on inbound messages during processing: `👀` received → `🤔` thinking → `🔥` tool active → `👍` done / `😱` error. Disable by setting explicitly to `false`. |
| `edit_in_place` | bool | `false` | Send a placeholder `…` message immediately, then edit it with the final response. Provides faster perceived response time. Recommended for production use. |

*`require_mention` is a plain `bool` field, so Go's zero value (`false`) is indistinguishable from "not set". This is a known issue — the safe default should be `true` but cannot be enforced by the type system. **Always set this field explicitly in your config.**

**Production-ready discord config:**
```toml
[channels.discord]
bot_token             = "${DISCORD_BOT_TOKEN}"
channel_ids           = ["YOUR_CHANNEL_ID"]
access_policy         = "invite"
require_mention       = true
rate_limit_per_minute = 30
reaction_status       = true
edit_in_place         = true
```

---

## `[tools]` — Tool execution

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `confirmation_timeout` | duration | `"24h"` | How long a pending tool confirmation remains valid before it expires and is auto-rejected. |
| `default_tool_timeout` | duration | `"30s"` | Maximum execution time for a single tool call. Tools that exceed this are cancelled and return a timeout error. Raise for long-running exec commands (e.g. `"120s"`). |
| `confirmation_rules` | array | `[]` | Rules for which tool calls require operator confirmation before execution. See below. |

### Confirmation rules

Confirmation rules match tool names and/or parameter patterns and block execution until a human approves via `chandra confirm <id>` or Discord reaction.

```toml
[[tools.confirmation_rules]]
pattern     = "exec"          # tool name (exact or prefix)
categories  = ["destructive"] # categories from the tool definition
description = "Shell command execution"

[[tools.confirmation_rules]]
pattern     = "write_file"
description = "File write operations"
```

Tier-2 high-risk exec patterns (pipe-to-shell, systemctl stop/disable, iptables, writes to /etc/ /usr/) trigger interactive Discord reaction approval regardless of `confirmation_rules`.

---

## `[actionlog]` — Action logging

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `llm_summaries` | bool | `false` | Use the LLM to generate natural-language summaries for action log rollups. Consumes tokens. Disable for cost-sensitive deployments. |

---

## `[skills]` — Skill system

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `path` | string | `"~/.config/chandra/skills"` | Root directory for skill files. Each skill is a subdirectory containing `SKILL.md`. |
| `priority` | float | `0.7` | Base priority for skill context candidates in the context budget. Higher = more likely to appear in context when space is tight. |
| `max_context_tokens` | int | `2000` | Maximum tokens allocated to all matched skill content combined. Skills are truncated if they exceed this budget. |
| `max_matches` | int | `3` | Maximum number of skills injected into context per turn. Skills are ranked by trigger match quality; only the top N are included. |
| `require_validation` | bool | `false` | Require all skill dependencies (binaries, env vars) to be present before the daemon starts. Useful for production; can block startup for optional skills in dev. |
| `auto_reload` | bool | `true` | Reload skill definitions from disk automatically when files change (inotify-based). Disable for immutable deployments. |

### `[skills.generator]`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `max_concurrent_generations` | int | `1` | Max simultaneous `write_skill` operations. |
| `generation_timeout` | duration | `"5m"` | Timeout for a single skill generation operation. |
| `max_pending_review` | int | `10` | Maximum number of `pending_review` skills before new generations are rejected. Prevents an unbounded queue of unapproved skills. |

---

## `[operator]` — Operator self-management

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `config_confirm_timeout_secs` | int | `30` | Seconds to wait for user confirmation after a cold config change (one requiring daemon restart). If the user does not respond in time, the change is auto-reverted and the backup config is restored. **Minimum: 15. Maximum: 120. Hard-clamped at startup.** |

Cold config changes (any value not classified as "hot") follow the Windows-resolution pattern: the new config is written, the daemon restarts, and a pending confirmation is stored in the DB. If the user replies with `yes`/`keep` within the timeout, the change is confirmed. If they reply `no`/`revert`, or the timeout fires, the `.bak` config is restored and the daemon restarts again.

This prevents remote config changes from locking you out of the bot.

---

## `[executor]`, `[plans]`, `[planner]`, `[infrastructure]`

These sections control the plan execution and infrastructure management subsystems. They are not required for basic operation and can be omitted. Default values are used when absent.

| Section | Field | Default | Description |
|---------|-------|---------|-------------|
| `[executor]` | `parallel_steps` | `false` | Execute plan steps in parallel where dependencies allow. |
| | `rollback_on_failure` | `false` | Auto-rollback completed steps if a later step fails. |
| | `max_concurrent_plans` | `2` | Max simultaneously executing plans. |
| | `max_concurrent_steps` | `3` | Max parallel steps within a single plan. |
| | `step_timeout` | `"10m"` | Max execution time per plan step. |
| `[plans]` | `auto_rollback_idempotent` | `false` | Automatically rollback idempotent operations on failure. |
| | `notification_retention` | `"168h"` | How long to retain plan completion notifications. |
| `[planner]` | `max_steps` | `20` | Maximum steps per generated plan. |
| | `checkpoint_timeout` | `"24h"` | How long a paused plan checkpoint remains valid. |
| | `allow_infra_creation` | `true` | Allow plans to create new infrastructure. |
| | `allow_software_install` | `true` | Allow plans to install software. |
| `[infrastructure]` | `discovery_interval` | `""` | How often to re-discover infrastructure topology. |
| | `max_concurrent_hosts` | `0` | Max simultaneous host connections during discovery. |
| | `host_timeout` | `"30s"` | Per-host connection timeout. |
| | `cache_ttl` | `"5m"` | How long to cache discovered infrastructure state. |

---

## Full example: single-user production config

```toml
# ~/.config/chandra/config.toml
# Permissions: chmod 0600 ~/.config/chandra/config.toml

[identity]
name          = "Chandra"
persona_file  = "~/.config/chandra/persona.md"
max_tool_rounds = 10

[provider]
type          = "openrouter"
base_url      = "https://openrouter.ai/api/v1"
default_model = "anthropic/claude-sonnet-4-6"
# api_key goes in secrets.toml

[embeddings]
base_url   = "http://localhost:11434/v1"
model      = "nomic-embed-text"
dimensions = 768

[database]
path = "~/.config/chandra/chandra.db"

[budget]
semantic_weight     = 0.4
recency_weight      = 0.3
importance_weight   = 0.3
recency_decay_hours = 168

[scheduler]
tick_interval = "60s"

[mqtt]
mode = "disabled"

[channels.discord]
bot_token             = "${DISCORD_BOT_TOKEN}"
channel_ids           = ["YOUR_CHANNEL_ID"]
access_policy         = "invite"
require_mention       = true
rate_limit_per_minute = 30
reaction_status       = true
edit_in_place         = true

[tools]
confirmation_timeout = "24h"
default_tool_timeout = "120s"

[skills]
path               = "~/.config/chandra/skills"
max_matches        = 3
max_context_tokens = 2000
auto_reload        = true

[operator]
config_confirm_timeout_secs = 30
```

```toml
# ~/.config/chandra/secrets.toml
# Permissions: chmod 0600 ~/.config/chandra/secrets.toml

[provider]
api_key = "sk-or-v1-..."

[channels.discord]
bot_token = "Bot MTQ..."
```

---

## Hot vs cold config changes

Chandra classifies config fields into hot (applied at runtime without restart) and cold (require restart). When you use the `set_config` tool, cold changes trigger the confirmation window.

**Hot fields** (take effect immediately):
- `identity.max_tool_rounds`
- `channels.discord.require_mention`
- `channels.discord.reaction_status`
- `channels.discord.edit_in_place`
- `channels.discord.rate_limit_per_minute`

**Cold fields** (require restart + confirmation):
- All `[provider]` fields
- All `[embeddings]` fields
- `[database].path`
- `[channels.discord].channel_ids`, `bot_token`, `access_policy`
- All `[mqtt]` fields
- All `[scheduler]` fields

When a cold change is applied via `set_config`, the daemon restarts automatically and you have `config_confirm_timeout_secs` to confirm the change. If you do not confirm, the previous config is restored.
