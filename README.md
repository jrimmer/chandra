# Chandra

**A personal AI agent with structured, persistent memory.**

Chandra is a self-hosted AI agent runtime written in Go. It gives an LLM a real memory system — one that retrieves the right context on every turn, persists across restarts, and improves with use — alongside a scheduler that lets the agent act without being asked.

Designed by [Sal](https://github.com/openclaw/openclaw), an AI assistant, for autonomous long-term operation. See [`docs/plans/core-design-v1.md`](docs/plans/core-design-v1.md) for the full design rationale.

---

## Why Chandra

Most AI agent frameworks treat memory as session state: a context window that fills up, gets truncated, and resets on restart. Chandra treats memory as infrastructure: four typed, queryable layers — episodic, semantic, intent, and identity — with structured retrieval on every turn.

Key design choices:

- **Single Go binary.** No Node runtime, no Python environment, no Docker required. `chandrad` is the daemon; `chandra` is the CLI.
- **SQLite-native.** Everything — memory, sessions, intents, action logs, skill metadata — lives in one local SQLite file. WAL mode, atomic writes, 0 external dependencies at runtime.
- **Privacy-first embeddings.** Semantic search uses locally-hosted Ollama models by default. No conversation content leaves your machine for the embedding step.
- **Hybrid memory retrieval.** BM25 full-text search (SQLite FTS5) and vector KNN (sqlite-vec) run concurrently and merge via Reciprocal Rank Fusion. Keyword recall and semantic similarity work together rather than competing.
- **Proactive by design.** The scheduler injects turns into the agent loop without any inbound message. Chandra can initiate, follow up, and notice things independently.

---

## Features

### Summer Stop — Router-Level Safety Interrupt

The interrupt described above. Key implementation details for the technically inclined:

- Lives in the **router goroutine** in `cmd/chandrad/main.go` — runs independently of the agent loop goroutine
- Each conversation tracks a `convState` with a `cancelTurnFn` and a `sync.Mutex`; the router calls `cancelCurrentTurn()` which is safe at any time (no-op if nothing is active)
- The agent loop receives `context.Canceled` and interprets it as a clean cancellation (not an error), editing the placeholder message to "🛑 Cancelled."
- Stop signal detection casts wide intentionally: "stop that", "cancel it", "!stop", "forget it", "nevermind" all trigger. A false positive (unnecessary stop) is far less harmful than a false negative (can't stop a runaway task)

### Parallel Agent Execution — Worker Pool

Chandra supports spawning parallel sub-agents mid-turn to execute independent subtasks concurrently:

```
# Both workers launch simultaneously — tool calls in the same turn run in parallel
spawn_agent("Audit server-1 at 10.1.0.10 for open ports and failed logins")
spawn_agent("Audit server-2 at 10.1.0.11 for open ports and failed logins")

# Collect both results
await_agents([worker_1_id, worker_2_id])
```

Workers run the full LLM reasoning loop with their own context window and tool access. An inactivity watchdog (5-minute default, configurable) kills stalled workers without disrupting active ones. The Summer Stop cancels all workers automatically via context propagation — spawned agents cannot outlive a stop signal.

**Worker tool access:** `exec`, `read_file`, `write_file`, `web_search`, `get_current_time`, `read_skill`. Workers cannot spawn sub-workers (no recursion), modify config, or write to memory.

**Pool limit:** 3 concurrent workers (configurable via `identity.max_workers`).

This is Chandra's **third layer of concurrency**:

| Layer | Scope | Mechanism |
|---|---|---|
| 1 — Conversation parallelism | Multiple active conversations | One goroutine + channel per conversation |
| 2 — Tool execution | Tool calls within a single turn | `tools.Executor` dispatches all calls concurrently |
| 3 — Worker pool | Isolated sub-agents spawnable mid-turn | `internal/agent/worker.Pool` |

### Memory — Four Layers, Active Retrieval

Memory retrieval happens inside the think cycle on every turn, not just at session start.

**Episodic** — append-only conversation history, cross-session. The agent's short-term recall; the last 20 turns are loaded as fixed context on every call.

**Semantic** — embedding-based long-term memory. Every substantive turn is embedded and stored. On each turn, the top semantically-relevant memories are retrieved and ranked into context. Retrieval uses hybrid BM25 + vector KNN with Reciprocal Rank Fusion — keyword matches and semantic similarity both contribute, with neither dominating.

- Local embeddings via Ollama (e.g. `nomic-embed-text`) — no data leaves the machine
- `sqlite-vec` for SIMD-accelerated KNN at any practical memory scale
- SQLite FTS5 for BM25 keyword recall — especially effective for names, codes, and exact phrases
- RRF fusion (k=60) merges both ranked lists without requiring normalised score scales
- Importance scoring: reinforced memories (containing "remember" or "important") stored at 0.8; others at 0.5 based on content heuristics

**Intent** — persistent goals with scheduling metadata. If the agent commits to watching something or following up on a task, that intent survives restarts and lives in a dedicated SQLite table — not in session context that gets truncated.

**Identity** — typed agent profile, user profile, and relationship state. `trust_level`, `communication_style`, `last_interaction`, and `ongoing_context` are stored as structured data and loaded as the highest-priority system prompt on every turn.

### Agent Runtime

**9-step think-act-remember cycle:**

1. Load recent episodes (episodic, cross-session)
2. Load identity candidate (agent profile + relationship state)
3. Query semantic memory for relevant context
4. Match skills to message
5. Assemble context window within token budget
6. Call LLM provider (with tool loop, up to 10 rounds)
7. Append exchange to episodic store
8. Conditionally store to semantic memory
9. Update relationship state + action log

Steps 7–9 run in a background goroutine after the response is ready, so the user sees the reply without waiting for memory writes and re-embedding.

**Session management** — per-user sessions with 30-minute inactivity timeout; stable conversation IDs derived from `sha256(channel_id + ":" + user_id)` give consistent context without unbounded state growth.

**Message dispatch** — cross-conversation parallel, within-conversation serial. Each conversation has a dedicated buffered channel and one worker goroutine. Turn N+1 starts only after turn N completes, so episodic memory from N is always visible when assembling context for N+1. Different conversations run concurrently without head-of-line blocking.

**Proactive turns** — the scheduler injects `ScheduledTurn` values into the same agent loop. The agent can initiate contact, deliver reminders, and act on intents without any inbound message.

### Progressive Delivery

The agent's thinking state is surfaced to the user in real time, without waiting for the final response:

- **L0 — Reaction status:** emoji reactions on the inbound message (`👀` received → `🤔` thinking → `🔥` tool active → `👍` done / `😱` error)
- **L1 — Typing indicator:** Discord typing indicator active during LLM calls
- **L2 — Edit-in-place:** a placeholder message sent immediately, edited as the turn progresses; tool status lines accumulate in the message body

Tool status text is resolved from the active tool calls: `👨‍💻 Using exec…`, `⚡ Using web_search…`, etc. Stall indicators fire at 10s (`🥱`) and 30s (`😨`) if no activity is observed. A 700ms debounce prevents flicker on fast turns.

### `!` Command System

Instant commands processed before the LLM sees the message — no token cost, no round-trip:

| Command | Effect |
|---|---|
| `!help` | List all commands |
| `!reset` | Clear conversation context |
| `!retry` | Re-run the previous turn |
| `!status` | Daemon and provider status |
| `!context` | Show current context summary |
| `!skills` | List loaded skills |
| `!sessions` | List active conversations |
| `!usage` | Token usage statistics |
| `!quiet` | Toggle QUIET suppression |
| `!model <id>` | Override LLM model for this conversation |
| `!verbose` | Toggle verbose tool output |
| `!reasoning` | Toggle extended reasoning mode |

Skills can also declare custom `!` commands in their YAML frontmatter. Built-ins always take precedence.

### Interactive Exec Approval

A two-tier gate for shell command execution:

**Tier 1 — Hard block:** patterns that are categorically off-limits regardless of context. Commands matching these are rejected immediately with an explanation.

**Tier 2 — Interactive approval:** high-risk but potentially legitimate commands pause execution and post an approval prompt to the Discord channel. The operator reacts with ✅ (approve) or ❌ (deny) within 60 seconds. The message is edited with the outcome. Patterns include: pipe-to-shell (`| bash`, `| sh`), `systemctl stop/disable`, `iptables`, `ufw`, writes to `/etc/` and `/usr/`.

Approvals are gated to `allowed_users` — only authorised operators can approve.

### Context Budget Manager

The CBM owns the token budget and decides what enters the LLM call. Candidates are scored by semantic similarity, recency decay, and importance. Fixed candidates (identity, recent episodes) are guaranteed; ranked candidates (semantic memories, matched skills) are best-effort and dropped first under pressure.

Nothing gets into the context window without earning its place.

### Skills

Skills are SKILL.md files with YAML frontmatter. They provide context, instructions, and tool access to the agent for a specific domain.

```yaml
---
name: weather
triggers: [weather, forecast, temperature]
commands:
  - name: forecast
    description: "Show weather forecast"
---
You have access to web_search. Use it to look up current weather...
```

- Installed to `~/.config/chandra/skills/<name>/SKILL.md`
- Matched by keyword triggers on each turn; top matches injected as ranked context
- `chandra skill list / show / approve / reject / reload` — full lifecycle management
- `write_skill` tool — the agent can draft a new skill conversationally; it's saved as `pending_review` until you approve it with `chandra skill approve <name>`
- Skills can declare custom `!` commands in frontmatter; `SyncSkillCommands()` registers them at load time
- Self-healing: `SK5` detects persona/skill config drift on startup and repairs it

**Built-in skills:**
- `web` — DuckDuckGo search for grounding responses in current information
- `homeassistant` — get/set entity state for home automation
- `mqtt` — publish to the event bus for device control or inter-system signalling
- `context` — `note_context` and `forget_context` tools for the agent to manage its own `ongoing_context` store

### Scheduling & Events

**Intent scheduler** — tick-based evaluation of due intents. Survives restarts: intents live in SQLite, not memory. On boot, the scheduler loads all active intents and resumes.

**Natural language scheduling** — the `schedule_reminder` tool lets the agent create intents from conversation: "remind me in 20 minutes" creates a one-shot intent delivered to the right channel and user.

**Event bus** — internal pub/sub with MQTT-style topic wildcards, worker pool, and bounded queue with priority support.

**MQTT bridge** — embedded broker or external client. External events (device state, monitoring alerts, webhooks) can trigger agent reasoning without any user message.

### Access Control

- `chandra invite create --ttl 24h --uses 5` — generate time-limited invite codes
- Users redeem codes to join the allowlist; invite use is tracked per-redemption
- `chandra access list / revoke` — manage the allowlist
- Unauthorized messages are silently dropped (no response, no error visible to the sender)
- `allowed_users = []` is a hard error at daemon startup — the bot will not run locked out of itself
- Per-channel tool allowlists — restrict which tools are available in which channel

### Channels & Providers

**Channels:**
- Discord — bot adapter with privileged intent (`MessageContent`), prompt injection detection, message deduplication, reaction-based approval workflow
- Architecture supports additional channel adapters; the agent loop is channel-agnostic

**LLM Providers:**
- Anthropic, OpenAI, Ollama, any OpenAI-compatible endpoint
- Provider is configured per-deployment; switching is a config change, not a code change
- Custom base URLs validated: HTTPS required, RFC-1918 blocked (SSRF guard), loopback exempt

**Embedding Providers:**
- Separate from the chat provider — local Ollama and cloud embeddings can coexist
- LRU embed cache (256 entries, 5-minute TTL) — repeated queries skip the Ollama round-trip (~100ms saved per cache hit)

### Operations

**`chandra init`** — interactive setup wizard. Guides through provider selection, API key validation, Discord bot setup, Hello World verification loop, and access control bootstrap. Checkpoint-resumable; saves progress across interruptions.

**`chandra doctor`** — 8-check health verification: config permissions, binary, daemon connectivity, provider reachability, DB integrity, scheduler state, channel connectivity, allowlist sanity. Exits non-zero on hard failures, warns on soft issues. Completes in under 1 second on a healthy install.

**`chandra security audit`** — explicit security posture check: file permissions, HTTPS enforcement, open policy detection, secret exposure in config.

**`chandra status` / `chandra health`** — daemon status and per-subsystem health including provider reachability probe (TCP dial with 3-second timeout).

**`chandra memory search <query>`** — search semantic memory from the CLI.

**`chandra intent list / add / complete`** — manage the scheduler's work queue.

**`chandra log --today`** — query the action log with filtering.

**`chandra confirm <id>`** — approve a pending tool confirmation from the terminal.

**Missed-job recovery** — on startup, the scheduler detects overdue recurring intents (daemon was offline during a scheduled window) and reschedules them with a 5-second stagger. One-shot reminders are untouched and fire on the first tick. Prevents a burst of simultaneous LLM calls when the daemon restarts after downtime.

**Per-user rate limiting** — configurable via `channels.discord.rate_limit_per_minute`. Exceeded messages are dropped silently (no reply, warning logged). Default `0` = unlimited. Recommended: `30` for multi-user deployments.

### Security

- Config files at `0600`, config directory at `0700`, enforced at startup and on every write
- Secrets isolated to `secrets.toml`; daemon refuses to start if permissions are wrong
- Tool confirmation gate — destructive, external, and financial operations block until explicit human approval; rules defined in config (tools cannot bypass)
- Interactive exec approval — tier-2 shell commands require operator reaction approval via Discord
- Prompt injection detection — tool call names found verbatim in user input are filtered before execution
- HTTPS required for all non-local providers; RFC-1918 addresses blocked (SSRF guard)
- Unix socket API at `0600` — CLI communication without network exposure
- **Summer Stop** — router-level interrupt that cannot be blocked by the agent under any circumstances
- Per-user rate limiting — configurable token bucket (default unlimited); protects against accidental loops and multi-user flooding
- Missed-job recovery — staggered restart prevents LLM call storms after downtime

---

## How Chandra Compares to OpenClaw

[OpenClaw](https://github.com/openclaw/openclaw) is a mature personal AI assistant platform. They solve adjacent but different problems, and are worth understanding side by side.

| | **OpenClaw** | **Chandra** |
|---|---|---|
| **Language** | TypeScript / Node.js | Go |
| **Install** | `npm install -g openclaw` | Single compiled binary |
| **Primary focus** | Multi-channel communication layer | Structured memory + agent runtime |
| **Channels** | 20+ (WhatsApp, Telegram, Signal, Discord, Slack, iMessage, Matrix, IRC, and more) | Discord (extensible architecture) |
| **Memory model** | Markdown files + keyword search (configurable with add-ons) | 4-layer typed system: episodic, semantic (hybrid BM25+vector), intent, identity |
| **Embeddings** | External provider | Local Ollama by default; external optional |
| **Skill ecosystem** | ClawHub marketplace; AI coding agent integration (Codex, Claude Code) | SKILL.md files; conversational skill generation via `write_skill` tool |
| **Scheduling** | Cron jobs via gateway | Intent store with natural-language scheduling |
| **Setup** | `openclaw onboard` wizard | `chandra init` wizard |
| **Config** | JSON/TOML gateway config + workspace files | TOML with atomic SafeWriter |
| **Database** | External (varies) | SQLite, single file, zero external dependencies |
| **Safety interrupt** | — | Summer Stop: router-level, agent cannot resist |
| **Parallel agents** | — | Worker pool: `spawn_agent` / `await_agents` |
| **Target user** | Someone who wants their AI connected to every surface they use | Someone who wants an agent that remembers well and can run locally |

**OpenClaw** excels at being everywhere: 20+ channels, voice, canvas, a Node.js skill ecosystem, AI coding agent integration, and a growing community. If you want your AI assistant accessible from WhatsApp, iMessage, Slack, Matrix, and IRC simultaneously — OpenClaw is the right tool.

**Chandra** excels at remembering: hybrid BM25+vector retrieval surfaces the right context across long conversations, a 4-layer memory architecture keeps facts, goals, and identity typed and queryable, and everything runs locally with no external data dependencies for the memory layer. If you want an agent that gets more useful over time and respects your privacy — Chandra is designed around that.

They're also composable: Chandra was originally designed as a companion to OpenClaw, inspired by the same goal of making AI assistants genuinely personal. Using both is a reasonable setup.

---

## Quick Start

### Prerequisites

- Go 1.22+
- CGO enabled (gcc or clang)
- SQLite3 development headers (`libsqlite3-dev` on Debian/Ubuntu)
- Ollama (for local embeddings) — optional but recommended

### Build

```bash
git clone https://github.com/jrimmer/chandra.git
cd chandra
make build          # outputs bin/chandrad and bin/chandra
sudo make install   # installs to /usr/local/bin
```

Or manually:
```bash
CGO_ENABLED=1 go build -tags sqlite_fts5 -o bin/chandrad ./cmd/chandrad
CGO_ENABLED=1 go build -tags sqlite_fts5 -o bin/chandra  ./cmd/chandra
```

> **Note:** The `sqlite_fts5` build tag enables FTS5 full-text search in go-sqlite3, which powers hybrid BM25+vector memory retrieval. Without it the binary falls back to pure vector search.

### Setup

```bash
chandra init
```

The wizard guides through:
1. Provider selection and API key validation
2. Discord bot setup and channel verification
3. Identity configuration (agent name and persona)
4. Hello World verification loop (confirms end-to-end message delivery)
5. `chandra doctor` to verify the full stack

### Run

```bash
chandrad           # start daemon (foreground)
chandra health     # verify everything is healthy
```

---

## Configuration

Configuration lives at `~/.config/chandra/`:

```
~/.config/chandra/
├── config.toml      # main config (0600)
├── secrets.toml     # API keys (0600)
├── chandra.db       # SQLite database
└── skills/          # installed skills
    └── weather/
        └── SKILL.md
```

Minimum `config.toml`:

```toml
[provider]
type          = "anthropic"
default_model = "claude-sonnet-4-6"

[embeddings]
base_url   = "http://localhost:11434/v1"
api_key    = ""
model      = "nomic-embed-text"
dimensions = 768

[channels.discord]
token       = "Bot your-token-here"
channel_ids = ["your-channel-id"]

[access]
policy = "allowlist"
```

Secrets in `secrets.toml`:

```toml
[provider]
api_key = "sk-ant-..."
```

---

## CLI Reference

```bash
# Setup & health
chandra init                     # interactive setup wizard
chandra doctor                   # 8-check health verification
chandra security audit           # security posture check
chandra status                   # daemon status
chandra health                   # per-subsystem health

# Memory
chandra memory search <query>    # search semantic memory

# Skills
chandra skill list               # list installed skills
chandra skill show <name>        # show skill content
chandra skill pending            # list skills awaiting approval
chandra skill approve <name>     # approve a pending skill
chandra skill reject <name>      # reject a pending skill
chandra skill reload             # reload all skills from disk

# Access control
chandra invite create            # generate invite code
chandra invite list              # list active codes
chandra access list              # show allowlist
chandra access revoke <user>     # remove user from allowlist

# Intents & scheduling
chandra intent list              # show active intents
chandra intent add <description> # create an intent
chandra intent complete <id>     # mark intent complete

# Action log
chandra log --today              # today's action log
chandra log --since 2h           # last 2 hours

# Confirmations
chandra confirm <id>             # approve a pending tool call
chandra confirm list             # list pending confirmations
```

---

## Testing

```bash
make test             # full suite with sqlite_fts5 tag
make test-all         # full suite + race detector

# Specific packages
go test -tags sqlite_fts5 ./internal/memory/semantic/... -v
go test -tags sqlite_fts5 ./tests/integration/... -run TestDesignIntent -v
go test -tags sqlite_fts5 ./tests/integration/... -run TestMemory_ -v
```

The test suite uses real SQLite (WAL mode) and mock LLM providers — no network calls required. See [`TESTING.md`](TESTING.md) for the full structured test plan including semantic memory testing strategy, adversarial scenarios, and chaos tests.

---

## Architecture

The agent loop is the centre of gravity. Everything else — channels, tools, scheduler, memory — is either a source of turns or a resource the loop consumes.

```
Discord ─────────────────────┐
                             │   ← Stop signal? → cancelCurrentTurn() immediately
                             ▼         (Summer Stop — runs here, outside agent loop)
Scheduler ──────────► Router goroutine ──► [conv A] chan → worker goroutine
                                           [conv B] chan → worker goroutine
                                           [conv C] chan → worker goroutine

                      Worker goroutine:
                      Agent loop (Run)
                      ├── 1. episodic recall
                      ├── 2. identity candidate
                      ├── 3. semantic query (BM25 + vector + RRF)
                      ├── 4. skill matching
                      ├── 5. context budget assembly
                      ├── 6. LLM call + tool loop
                      │   ├── spawn_agent ──► worker pool goroutine 1
                      │   ├── spawn_agent ──► worker pool goroutine 2  ← Layer 3
                      │   └── await_agents (blocks until all complete)
                      └── 7-9. post-process (background goroutine)
                              ├── episodic append
                              ├── semantic store
                              └── relationship update
```

Full design documentation:
- [`docs/plans/core-design-v1.md`](docs/plans/core-design-v1.md) — core architecture
- [`docs/plans/autonomy-design-v1.md`](docs/plans/autonomy-design-v1.md) — autonomy and skill systems
- [`docs/plans/reliability-design-v1.md`](docs/plans/reliability-design-v1.md) — reliability and observability
- [`WORKERS.md`](WORKERS.md) — parallel agent execution design
- [`SAFETY-INTERRUPT.md`](SAFETY-INTERRUPT.md) — Summer Stop implementation details
- [`CONFIG.md`](CONFIG.md) — complete configuration reference with defaults and recommendations

---

## Status

Chandra is in active development. The core agent runtime is complete and running in production. Phase 2 (console TUI, additional channels, request policy) is underway.

**What's working today:**
- Full agent loop with 4-layer memory and hybrid retrieval
- Discord channel with progressive delivery (reactions, typing, edit-in-place)
- Summer Stop safety interrupt
- Parallel worker pool (`spawn_agent` / `await_agents`)
- `!` command system (12 built-ins + skill extensibility)
- Interactive exec approval via Discord reactions
- Auto-update system (self-modifying agent loop)
- `chandra doctor` 8/8 health checks passing
- T1/T2/T3 chaos and integration test suites

See [`BACKLOG.md`](BACKLOG.md) for the prioritised roadmap.

---

## License

MIT
