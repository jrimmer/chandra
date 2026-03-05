# Chandra

A personal AI agent runtime with structured memory, event-driven triggers, and Discord integration.

Designed by Sal, just such an agent! See [docs/core-design-v1.md](docs/core-design-v1.md) for the full design rationale.

## Implemented Features

### Memory (4-layer)

Memory is active, not passive. Retrieval happens inside the think cycle on every turn, not just at session start. Relevant memories surface mid-conversation when context triggers them.

- **Episodic** — append-only conversation history per session; the agent's short-term recall of what was just said
- **Semantic** — embedding-based retrieval (SQLite + sqlite-vec); memory is loaded at session start as a blob of markdown in most frameworks and never updated mid-conversation — here, a similarity query surfaces the most relevant memories on every turn
- **Intent** — persistent goals with scheduling metadata; if the agent is asked to watch something or follow up on a task, that intent lives in a dedicated SQLite table, not in session context that gets lost on restart or truncation
- **Identity** — typed agent profile, user profile, and relationship state (trust level, communication style); Go structs stored in SQLite and loaded as structured data, not narrative text — because agent persona and user context as flat markdown files aren't queryable, aren't typed, and aren't maintained

### Agent Runtime

Proactive over reactive. The scheduler can inject intent into the agent loop without any inbound message. Chandra can initiate, follow up, and notice things independently.

- **Reactive + proactive agent loop** — 9-step think-act-remember cycle processes both user messages and scheduler-injected turns through the same path
- **Session management** — per-user sessions with 30-minute inactivity timeout; stable conversation IDs via SHA-256 hash give consistent context without unbounded state growth
- **Message dispatch** — cross-conversation parallel, within-conversation serial (see [Message Dispatch Model](#message-dispatch-model) below)
- **Graceful lifecycle** — 15-step ordered startup, signal-aware shutdown in reverse; 10-second startup watchdog with config rollback on DB failure

### Context Budget Manager

Context is managed, not assumed. The CBM owns the token budget and decides what gets included in each LLM call — ranking recency, relevance, and importance. The agent never blindly dumps context.

- **Token-aware context assembly** — scores candidates by semantic similarity, recency decay, and importance, then greedily packs the context window within the model's limit; nothing gets in without earning its place

### Tool System

Tools are observable. Every tool call is tracked — latency, success/failure, error patterns. The runtime builds a reliability model over time and can factor that into decisions. This replaces advisory security (behavioral tiers that degrade under long sessions) with declared capability tiers validated at dispatch.

- **Registry with capability enforcement** — tools declare capabilities (network, file, memory); the runtime validates before dispatching — catches accidental capability violations and provides audit logging
- **Parallel execution with retry** — goroutine-dispatched tool calls with per-call timeouts and transient-error retries
- **Confirmation gate** — pattern-matched dangerous operations (destructive, external sends, financial) block until human approval via CLI; rules are defined in config, not by tools themselves, so tools cannot bypass confirmation
- **Telemetry** — latency, success/failure, error messages, and call frequency recorded per tool; builds a reliability model over time

### Scheduling & Events

Intent survives restarts. Active tasks, watch items, and persistent goals live in the Intent store, not in memory. The scheduler reads them on boot. Nothing is lost across restarts.

- **Intent scheduler** — tick-based evaluation of due intents; runs independently of the inbound message path, injecting reasoning turns into the agent loop without any external trigger
- **Event bus** — internal pub/sub with MQTT-style topic wildcards, worker pool, and bounded queue with priority support
- **MQTT bridge** — embedded broker or external client; events can fire agent reasoning independently of inbound user messages
- **Event-to-intent handler** — converts external events into intents with 5-minute deduplication window; bridges external events into the agent's scheduling loop

### Channels
- **Discord** — bot adapter with prompt injection detection, message deduplication, and threaded replies; architecture supports additional channels

### LLM Providers
- **Anthropic, OpenAI, Ollama** — OpenAI-compatible HTTP interface; local models and cloud providers are interchangeable via config, no code changes
- **Embedding provider** — separate from chat provider because chat and embeddings often use different endpoints (e.g., Claude for chat, OpenAI for embeddings)

### Skills (Built-in Tools)
- **Web search** — DuckDuckGo Lite scraper for grounding responses in current information
- **Home Assistant** — get/set entity state for home automation control
- **MQTT publish** — push messages to the event bus for device control or inter-system signaling

### Operations
- **Action log with rollups** — audit trail of every agent action with hourly aggregation; full observability into what the agent did and why
- **Unix socket API** — JSON-RPC 2.0 server with 20+ methods; daemon and CLI communicate without network exposure
- **CLI client** — `chandra` binary for health checks, memory search, intent management, log queries, and confirmation approvals
- **Atomic config** — TOML with SafeWriter (3-generation backup rotation, atomic rename); startup watchdog rolls back on DB failure
- **Safe mode** — minimal startup with no external connections for debugging

## Prerequisites

- Go 1.22+
- CGO enabled (C compiler required: gcc or clang)
- SQLite3 development headers

## Build

    CGO_ENABLED=1 go build ./...

## Configuration

Copy the example config and edit:

    mkdir -p ~/.config/chandra
    chmod 700 ~/.config/chandra
    # Create ~/.config/chandra/config.toml with your settings
    chmod 600 ~/.config/chandra/config.toml

Minimum config:

    [provider]
    type = "anthropic"
    api_key = "sk-ant-..."
    model = "claude-sonnet-4-6"

    [embeddings]
    base_url = "https://api.openai.com/v1"
    api_key = "sk-..."
    model = "text-embedding-3-small"

    [channels.discord]
    token = "Bot ..."
    channel_ids = ["your-channel-id"]

## Run

    CGO_ENABLED=1 go run ./cmd/chandrad

## CLI Commands

    chandra health            # Health check
    chandra memory search <q> # Search memory
    chandra intent list       # List active intents
    chandra intent add <desc> # Add intent
    chandra log --today       # Today's action log
    chandra confirm <id>      # Approve a pending tool call

## Testing

    CGO_ENABLED=1 go test ./...


## Message Dispatch Model

**Problem:** A personal AI agent needs two competing properties:
1. A slow or hanging LLM call for one user must not starve other users
2. Sequential messages from the same user must be processed in order, with each turn seeing the prior turn's episodic memory

A naive goroutine-per-message approach satisfies (1) but breaks (2): messages 1 and 2 in the same conversation run concurrently, both assembling context at the same moment before either has written to episodic memory. The result is stale context and potentially out-of-order replies.

A single sequential goroutine satisfies (2) but breaks (1): one slow LLM call for user A freezes every message behind it, regardless of which user sent them.

**Solution: cross-conversation parallel, within-conversation serial**

```
inbound messages
      |
      v
  router goroutine
  (auth + session)
      |
      +--[conv A]-->  chan (buf 32)  -->  worker A (sequential)
      |
      +--[conv B]-->  chan (buf 32)  -->  worker B (sequential)
      |
      +--[conv C]-->  chan (buf 32)  -->  worker C (sequential)
```

- The router goroutine authenticates each message, resolves the session, and fans it into a per-conversation buffered channel (`convQueues` map)
- Each conversation gets exactly one worker goroutine, created on first message
- Worker goroutines drain their channel sequentially — turn N+1 starts only after turn N completes, so episodic memory from N is visible when assembling context for N+1
- Workers across different conversations run in parallel — user A's slow turn does not block user B
- Each turn has a 90-second context deadline; a hung LLM call times out and logs an error rather than leaking a goroutine
- Queue overflow (> 32 pending turns per conversation) logs a warning and drops the message rather than blocking the router

**Why not a worker pool?** A fixed pool of N workers would still allow two messages from the same conversation to run concurrently if the pool has spare workers. Per-conversation channels are the right unit of serialization.

## Architecture

See [docs/core-design-v1.md](docs/core-design-v1.md) for core architecture and [docs/autonomy-design-v1.md](docs/autonomy-design-v1.md) for autonomy systems.
