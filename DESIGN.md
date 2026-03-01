# Chandra — Design Document

> A Go-based personal AI agent runtime with structured memory, event-driven triggers,
> and a clean tool interface. Designed as a capable, self-aware daily driver that knows
> who it's helping and acts accordingly.

**Status:** Design phase  
**Last updated:** 2026-03-01  
**Binaries:** `chandrad` (daemon), `chandra` (CLI)

---

## Implementor Quick-Start

Critical notes for anyone implementing this spec. Read before writing a line of code.

- **Go module name:** `github.com/jrimmer/chandra`
- **Build requirement:** `CGO_ENABLED=1` — sqlite-vec requires CGO. All builds and CI must set this.
- **Start with `store/`** — the database layer is the foundation everything else depends on. Do not implement any other package until migrations run cleanly and sqlite-vec loads.
- **sqlite-vec `init()` pattern** (section 7, `store/` notes) is easy to overlook and silently breaks all vector queries if missed. It must be in an `init()` func before any `sql.Open()` call.
- **Coupled implementations — do these together, not separately:**
  - Intent store + Scheduler (sections covering gaps 2 and 6) share state and must be designed as a unit
  - Semantic store + Context Budget Manager (gaps 1 and 5) are two halves of the same retrieval system

---

## 1. Project Goals

### Vision

Chandra is not a generic AI agent framework. It is a personal AI runtime designed around a
specific, opinionated insight: the most effective AI assistant is one that has persistent
identity, genuine memory, proactive intent, and deep knowledge of the person it serves.
Most frameworks treat these as optional add-ons. Chandra treats them as the foundation.

The name reflects this — Chandra is a named entity with a defined character, not a nameless
runtime. The architecture enforces this: identity and relationship state are typed,
structured, first-class components, not prompt text loaded from a config file.

### Core Goals (v1)

1. **Persistent, queryable memory** — SQLite-backed with four distinct memory layers
   (episodic, semantic, intent, identity), each serving a different cognitive function
2. **Multi-provider LLM support** — OpenAI-compatible HTTP interface; local models
   (Ollama, LM Studio, vllm) and cloud providers are interchangeable via config
3. **Concurrent tool execution** — goroutine-based parallel tool dispatch with
   telemetry, retry awareness, and reliability tracking
4. **Event-driven triggers** — MQTT bridge + internal event bus; events can fire
   agent reasoning independently of inbound user messages
5. **Channel integrations** — Discord as primary surface (v1); architecture supports
   additional channels
6. **Skill/tool plugin system** — clean registration interface, explicit capability
   declarations, isolated execution
7. **First-class agent identity** — Chandra has a typed identity structure; the user has a
   typed profile; their relationship has tracked state — none of this is flat text

### Explicit Non-Goals (v1)

- Local model inference (use Ollama or any OpenAI-compatible endpoint instead)
- Multi-user support
- Web UI
- Training or fine-tuning
- Multi-agent orchestration (future)
- Horizontal scaling / clustering (future)

---

## 2. Architecture

### Overview

```
┌─────────────────────────────────────────────┐
│                   chandrad                  │
│                                             │
│  ┌──────────┐    ┌──────────────────────┐   │
│  │ Channels │───▶│   Session Manager    │   │
│  │ Discord  │    │  (goroutine/session) │   │
│  │ Telegram │    └──────────┬───────────┘   │
│  └──────────┘               │               │
│                             │               │
│  ┌──────────┐    ┌──────────▼───────────┐   │
│  │Scheduler │───▶│     Agent Loop       │   │
│  │(proactive│    │                      │   │
│  │ intents) │    │  1. assemble context │   │
│  └──────────┘    │  2. think (LLM)      │   │
│                  │  3. act (tools)      │   │
│                  │  4. remember         │   │
│                  │  5. surface memory   │   │
│                  └───┬──────┬───────────┘   │
│                      │      │               │
│           ┌──────────▼─┐  ┌─▼──────────┐   │
│           │  Provider  │  │   Tools    │   │
│           │  (LLM API) │  │ (registry, │   │
│           └────────────┘  │  telemetry)│   │
│                           └────────────┘   │
│                                             │
│  ┌──────────┐    ┌──────────────────────┐   │
│  │  Events  │    │       Memory         │   │
│  │  (MQTT)  │    │  ┌────────────────┐  │   │
│  └────┬─────┘    │  │    Episodic    │  │   │
│       │          │  ├────────────────┤  │   │
│       │          │  │    Semantic    │  │   │
│       └─────────▶│  ├────────────────┤  │   │
│                  │  │     Intent     │  │   │
│                  │  ├────────────────┤  │   │
│                  │  │    Identity    │  │   │
│                  │  └────────────────┘  │   │
│                  └──────────────────────┘   │
│                                             │
│  ┌─────────────────────────────────────┐    │
│  │       Context Budget Manager        │    │
│  │  ranks + assembles LLM context     │    │
│  └─────────────────────────────────────┘    │
└─────────────────────────────────────────────┘
```

### Key Data Flows

**Inbound message:**
Channel → Session Manager → Agent Loop → Context Budget Manager assembles context →
Provider (LLM) → Tool dispatch (parallel goroutines) → Memory write → Response → Channel

**Proactive trigger:**
Scheduler reads Intent store → injects into Agent Loop → same path as above,
no inbound message required

**External event:**
MQTT → Event Bus → Agent Loop (if a subscription matches) or Memory (log it)

**Memory retrieval (inside think cycle):**
Agent Loop → Semantic index query (embedding similarity) → ranked results →
Context Budget Manager includes top-N within token budget

### Design Principles

1. **Memory is active, not passive.** Memory retrieval happens inside the think cycle
   on every turn, not just at session start. Relevant memories surface mid-conversation
   when context triggers them.

2. **Proactive over reactive.** The Scheduler can inject intent into the agent loop
   without any inbound message. Chandra can initiate, follow up, and notice things
   independently.

3. **Identity is typed, not textual.** Agent and user profiles are Go structs stored
   in the Identity layer — not markdown files loaded into a system prompt. They inform
   every decision structurally.

4. **Intent survives restarts.** Active tasks, watch items, and persistent goals live
   in the Intent store (SQLite), not in memory. The Scheduler reads them on boot.
   Nothing is lost across restarts.

5. **Context is managed, not assumed.** The Context Budget Manager owns the token
   budget and decides what gets included in each LLM call — ranking recency, relevance,
   and importance. The agent never blindly dumps files into context.

6. **Tools are observable.** Every tool call is tracked — latency, success/failure,
   error patterns. The runtime builds a reliability model over time and can factor
   that into decisions.

---

## 3. The Six Core Gaps (vs OpenClaw)

These are the specific architectural shortcomings of the current OpenClaw-based setup
that Chandra is designed to solve. Each maps directly to a component in the architecture.

### Gap 1 — Memory is passive
**Problem:** Memory is loaded at session start as a blob of markdown and never updated
mid-conversation. Relevant past context doesn't surface unless the agent manually
searches for it.

**Solution:** The Semantic memory layer stores embeddings for all significant exchanges.
The Agent Loop includes a retrieval step inside the think cycle — on every turn,
a similarity query surfaces the most relevant memories within the context budget.
Memory is a live participant in reasoning, not a cold-start document.

### Gap 2 — No proactive reasoning
**Problem:** The agent is purely reactive. It responds to messages and cron pings but
cannot independently decide to initiate, follow up, or notice patterns over time.

**Solution:** The `Scheduler` component runs independently of the inbound message path.
It reads the Intent store, evaluates time and condition-based rules, and injects
reasoning turns into the Agent Loop without any external trigger. Chandra can decide
to reach out, check in, or act on its own.

### Gap 3 — Identity isn't structural
**Problem:** Agent persona (SOUL.md) and user context (USER.md) are flat text files
loaded into the system prompt. They're not queryable, not typed, and not maintained
— they're just more context window consumption.

**Solution:** The Identity memory layer holds typed Go structs for the agent profile
(name, persona traits, capabilities), user profile (name, timezone, preferences,
relationship history), and relationship state (trust level, communication style,
ongoing context). These are persisted in SQLite and loaded as structured data,
not narrative text.

### Gap 4 — Tool execution is opaque
**Problem:** Tools either succeed or fail. There's no tracking of latency, no awareness
of flaky tools, no cost accounting, and no way to make smarter decisions based on
tool history.

**Solution:** The Tool Registry wraps every execution with telemetry — recording
latency, success/failure, error messages, and call frequency. Over time this builds
a reliability model. The Agent Loop can use this to prefer more reliable tools,
apply smarter retry logic, and surface tool health issues.

### Gap 5 — Context window management is manual
**Problem:** What gets loaded into each LLM call is decided ad-hoc — memory files,
skill docs, recent messages — with no principled way to stay within token budgets
or prioritize what actually matters for the current turn.

**Solution:** The `Context Budget Manager` owns the token budget for every LLM call.
It receives candidates (recent messages, retrieved memories, identity context, tool
descriptions, active intents) and ranks them by recency, semantic relevance to the
current query, and importance score. It assembles the optimal context window
within the model's limit. Nothing gets in without earning its place.

### Gap 6 — No inter-session continuity of intent
**Problem:** If the agent is asked to watch something, follow up on a task, or maintain
a persistent goal, that intent lives only in the current session's context. A restart
or context truncation loses it entirely.

**Solution:** The Intent store is a dedicated SQLite table (separate from episodic
memory) that persists active tasks, watch conditions, follow-up triggers, and
long-running goals. It survives restarts. The Scheduler reads it on boot and
resumes where it left off. Intent is durable by design.

### Component coupling notes

Gaps 2 and 6 are deeply coupled: the Scheduler is only useful if the Intent store
gives it something durable to work with. They must be designed and implemented together.

Gaps 1 and 5 are two halves of the same system: the Semantic index decides *which*
memories are relevant; the Context Budget Manager decides *how many* fit within the
token budget. Both must be in place for either to work correctly.

### Integration points (coupled components)

These bidirectional dependencies must be understood before implementation:

```
┌─────────────────────────────────────────────────────────────┐
│              Scheduler ←→ Intent Store                      │
│                                                             │
│  Scheduler.tick():                                          │
│    intents := IntentStore.Due()     // read due intents     │
│    for intent in intents:                                   │
│      if evaluateCondition(intent):  // may call LLM         │
│        emit ScheduledTurn                                   │
│      IntentStore.Update(intent)     // write next_check     │
│                                                             │
│  Scheduler needs: IntentStore, Provider (for NL conditions) │
│  IntentStore needs: nothing (pure persistence)              │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│           Semantic Store ←→ Context Budget Manager          │
│                                                             │
│  AgentLoop.Run():                                           │
│    memories := SemanticStore.QueryText(msg, topN=10)        │
│    candidates := toContextCandidates(memories)              │
│    context := CBM.Assemble(budget, fixed, candidates)       │
│                                                             │
│  Additionally, CBM should boost priority for:               │
│    - Memories matching active intents (from IntentStore)    │
│    - Recent episodes from current session                   │
│                                                             │
│  SemanticStore needs: Provider (for embeddings)             │
│  CBM needs: SemanticStore results, IntentStore.Active()     │
└─────────────────────────────────────────────────────────────┘
```

Implement each pair as a unit. Do not implement Scheduler without Intent store
populated and queryable. Do not implement CBM without Semantic store returning
ranked results.

---

## 4. Package Structure

```
chandra/
├── cmd/
│   ├── chandrad/          — daemon entrypoint
│   └── chandra/       — CLI entrypoint
│
├── internal/
│   ├── agent/         — the reasoning loop
│   │   ├── loop.go    — think → act → remember cycle
│   │   ├── context.go — context assembly coordination
│   │   └── session.go — session lifecycle
│   │
│   ├── memory/        — all four memory layers
│   │   ├── memory.go  — top-level Memory interface
│   │   ├── episodic/  — what happened, when
│   │   ├── semantic/  — embedding-based retrieval
│   │   ├── intent/    — persistent goals and tasks
│   │   └── identity/  — agent + user profiles
│   │
│   ├── budget/        — context budget manager
│   │   └── budget.go  — token-aware context assembly
│   │
│   ├── provider/      — LLM abstraction
│   │   ├── provider.go   — Provider + EmbeddingProvider interfaces
│   │   ├── openai/       — OpenAI-compatible implementation
│   │   ├── anthropic/    — Native Anthropic implementation
│   │   └── embeddings/   — EmbeddingProvider implementations
│   │
│   ├── tools/         — tool registry + execution
│   │   ├── registry.go  — registration, lookup, tier enforcement
│   │   ├── executor.go  — parallel dispatch, timeout, retry
│   │   ├── telemetry.go — reliability tracking per tool
│   │   ├── sandbox/     — out-of-process runner + RPC server
│   │   └── confirm/     — confirmation gate for Tier 4 actions
│   │
│   ├── scheduler/     — proactive intent engine
│   │   ├── scheduler.go — runs independently, reads intent store
│   │   └── rules.go     — condition evaluation
│   │
│   ├── actionlog/     — audit trail for human review
│   │   ├── log.go     — ActionLog interface implementation
│   │   └── rollup.go  — rollup generation (template + optional LLM)
│   │
│   ├── events/        — event bus
│   │   ├── bus.go     — internal pub/sub
│   │   └── mqtt/      — MQTT bridge
│   │
│   ├── channels/      — inbound/outbound surfaces
│   │   ├── channel.go — interface
│   │   └── discord/   — Discord implementation
│   │
│   └── config/        — typed configuration
│       └── config.go
│
├── pkg/               — exported types for plugins
│   ├── tool.go        — base Tool interface
│   ├── tool_trusted.go — Tier 2 interface (declares capabilities)
│   ├── tool_sandbox.go — Tier 3 RPC contract
│   ├── event.go       — Event types
│   └── memory.go      — Memory types plugins can read
│
├── skills/            — built-in tool implementations
│   ├── web/
│   ├── homeassistant/
│   ├── mqtt/
│   └── ...
│
├── store/             — SQLite schema + migrations
│   └── migrations/
│
└── chandra.db             — runtime database (gitignored)
```

### Plugin Security Model

The current OpenClaw security model is behavioral — advisory tiers (Green/Yellow/Red)
that rely on the agent's in-context judgment. This degrades under long sessions and
complex tasks, and provides no technical enforcement.

Chandra replaces advisory security with **declared capability tiers validated at dispatch**.
Capabilities are declared at plugin registration. The runtime validates them before
dispatching tool calls. This is defense-in-depth for trusted code, not a hard security
boundary against malicious plugins.

**Important caveat:** In-process plugins (Tier 1/2) can still call Go's standard library
directly. The runtime cannot prevent a plugin from calling `os.WriteFile()` if it wants to.
The tier system catches accidental capability violations and provides audit logging, but
true isolation requires OS-level sandboxing not implemented in v1. For v1, assume all
compiled-in plugins are trusted code; the tiers prevent accidents, not attacks.

#### Tiers

**Tier 1 — Built-in (compiled in, full trust)**
Internal skills shipped with Chandra. Direct memory access, all channels, unrestricted
tool calls. Reserved for core functionality: memory tools, identity management,
scheduling, channel management.

**Tier 2 — Trusted plugins (in-process, declared capabilities)**
Go packages compiled in but must explicitly declare capabilities at registration.
Runtime validates declarations before dispatching tool calls — if a plugin declares
read-only memory access, the runtime rejects write attempts at the dispatch layer.
Note: this does not prevent the plugin from calling stdlib directly; it only gates
the runtime's own APIs. Suitable for well-understood, trusted integrations:
HomeAssistant, UniFi, Hetzner, Proxmox.

**Tier 3 — Isolated plugins (out-of-process, restricted interface)**
Separate binary communicating over local Unix socket RPC. The runtime passes only
sanitized context; the plugin cannot access memory stores or other runtime internals
directly. Note: v1 does not implement OS-level sandboxing (seccomp, namespaces).
Process isolation provides interface restriction, not security containment. For
truly untrusted code, run in a container or VM outside Chandra. Suitable for
plugins that benefit from process isolation (crash isolation, separate resource
limits) but are still fundamentally trusted.

**Tier 4 — Confirmation-required (registry-enforced, pattern-matched)**
Certain actions require explicit user confirmation regardless of plugin tier.
Enforced by the Registry via pattern matching rules in config — tools cannot
bypass confirmation by simply not declaring it.

Confirmation is triggered by:
- Tool name matching config patterns (e.g., `.*delete.*`, `.*send_email.*`)
- Tool category matching config categories (e.g., "destructive", "external")
- Explicit per-tool override in config

Default confirmation-required patterns (configurable):
- Destructive operations: `.*delete.*|.*remove.*|.*drop.*|.*truncate.*`
- External sends: `.*send_email.*|.*post_.*|.*publish.*`
- Financial: `.*payment.*|.*transfer.*|.*purchase.*`

#### Mapping to autonomy intent

| Former advisory tier | Chandra enforcement |
|----------------------|-----------------|
| 🟢 Green | Tier 1/2, no confirmation required |
| 🟡 Yellow | Tier 2, execution logged + reported prominently |
| 🔴 Red | Tier 4 confirmation gate, any plugin tier |

### Key design decisions

**`internal/` vs `pkg/`** — Internal packages cannot be imported by external code.
All core logic lives in `internal/`. `pkg/` exports only the interfaces plugins need
to implement — keeping the external surface area minimal and stable.

**Memory as a package tree** — Each of the four memory layers is its own sub-package
with distinct storage schema and retrieval semantics. Separation prevents layer
bleeding and allows each to evolve independently.

**`skills/` as built-in implementations** — Skills implement the `pkg/tool.go`
interface. No magic — they are Go packages that register themselves at startup.
External plugins follow the same pattern.

**`store/` owns the database** — All SQLite schema and migrations are centralised here,
shared across memory layers. One database, multiple tables, one migration path.

## 5. Core Interfaces

All interfaces are defined in Go. These are the contracts the entire system is built
around. Implementation details live in section 7; only shapes are defined here.

---

### Tool (`pkg/tool.go`)

The base interface all tools implement regardless of tier.

```go
type ToolTier int

const (
    TierBuiltin   ToolTier = 1
    TierTrusted   ToolTier = 2
    TierIsolated  ToolTier = 3  // out-of-process, restricted interface
)

type Capability string

const (
    CapMemoryRead    Capability = "memory:read"
    CapMemoryWrite   Capability = "memory:write"
    CapNetworkOut    Capability = "network:outbound"
    CapChannelSend   Capability = "channel:send"
    CapFileRead      Capability = "file:read"
    CapFileWrite     Capability = "file:write"
    CapProcessExec   Capability = "process:exec"
)

// Confirmation requirements are NOT declared by tools (tools could lie).
// Instead, the Registry enforces confirmation via pattern matching rules
// defined in config. See ConfirmationRule below.

type ToolDef struct {
    Name        string         // unique identifier, e.g. "homeassistant.set_state"
    Description string         // shown to LLM in tool selection
    Parameters  json.RawMessage // JSON Schema describing input parameters
    Tier        ToolTier
    Capabilities []Capability   // declared at registration, enforced by runtime
}

type ToolCall struct {
    ID         string
    Name       string
    Parameters json.RawMessage
}

// ToolErrorKind distinguishes retry-able from terminal errors
type ToolErrorKind int

const (
    ErrTransient ToolErrorKind = iota // network, timeout, rate limit — retry
    ErrBadInput                        // invalid parameters — don't retry
    ErrAuth                            // authentication failure — don't retry
    ErrNotFound                        // resource not found — don't retry
    ErrInternal                        // unexpected error — log, maybe retry once
)

type ToolError struct {
    Kind    ToolErrorKind
    Message string
    Cause   error
}

func (e *ToolError) Error() string { return e.Message }
func (e *ToolError) Unwrap() error { return e.Cause }

type ToolResult struct {
    ID      string
    Content string
    Error   *ToolError     // typed error, nil on success
    Meta    map[string]any // latency, retries, etc — populated by executor
}

type Tool interface {
    Definition() ToolDef
    Execute(ctx context.Context, call ToolCall) (ToolResult, error)
}
```

---

### Tool Registry (`internal/tools/registry.go`)

```go
// ConfirmationRule defines when a tool requires user confirmation.
// Rules are defined in config, NOT by tools themselves (prevents bypass).
type ConfirmationRule struct {
    Pattern     string   // regex matched against tool name, e.g. ".*delete.*", ".*drop.*"
    Categories  []string // or match by category: ["destructive", "external", "financial"]
    Description string   // shown to user when confirmation requested
}

type Registry interface {
    Register(tool Tool) error
    Get(name string) (Tool, bool)
    All() []ToolDef                    // returns definitions for LLM context
    EnforceCapabilities(call ToolCall) error // returns error if undeclared capability attempted
    RequiresConfirmation(call ToolCall) (bool, ConfirmationRule) // checks config rules, not tool declaration
}
```

Confirmation rules are loaded from config at startup:
```toml
[[tools.confirmation_rules]]
pattern = ".*delete.*|.*remove.*|.*drop.*"
categories = ["destructive"]
description = "This action will permanently delete data."

[[tools.confirmation_rules]]
pattern = ".*send_email.*|.*post_tweet.*|.*send_message.*"
categories = ["external"]
description = "This action will send a message externally."
```

The Registry matches rules against tool names — tools cannot bypass confirmation by simply not declaring it.

---

### Provider (`internal/provider/provider.go`)

The LLM abstraction. OpenAI-compatible by default; any endpoint that speaks the
OpenAI chat completions API satisfies this without code changes — just config.

```go
type Message struct {
    Role       string          // "system" | "user" | "assistant" | "tool"
    Content    string
    ToolCallID string          // populated for role="tool" responses
    ToolCalls  []ToolCall      // populated when model requests tool execution
}

type CompletionRequest struct {
    Messages    []Message
    Tools       []ToolDef      // available tools for this turn
    MaxTokens   int
    Temperature float32
    Stream      bool
}

type CompletionResponse struct {
    Message     Message
    ToolCalls   []ToolCall     // non-empty when model wants to call tools
    InputTokens  int
    OutputTokens int
    StopReason  string         // "stop" | "tool_calls" | "max_tokens"
}

type Provider interface {
    Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
    CountTokens(messages []Message, tools []ToolDef) (int, error)
    ModelID() string
}

// EmbeddingProvider is separate from Provider because chat and embeddings
// often use different endpoints/services (e.g., Claude for chat, OpenAI for embeddings).
type EmbeddingRequest struct {
    Texts []string
    Model string  // optional, uses config default if empty
}

type EmbeddingResponse struct {
    Embeddings [][]float32  // one embedding per input text
    Model      string
    Dimensions int
}

type EmbeddingProvider interface {
    Embed(ctx context.Context, req EmbeddingRequest) (EmbeddingResponse, error)
    Dimensions() int  // returns embedding dimensions for this model
}

// Note: EmbeddingProvider lives in internal/provider/, not pkg/. Plugins don't
// need to provide custom embeddings — they use the configured embedding service.
```

---

### Channel (`internal/channels/channel.go`)

Inbound and outbound surface abstraction. Discord is the v1 implementation.

```go
type InboundMessage struct {
    ID             string
    ConversationID string         // stable: hash(channel_id + user_id), set by channel
    UserID         string
    Content        string
    Timestamp      time.Time
    Meta           map[string]any // channel-specific: guild, thread, dm flag, etc.
}

// Note: Channels set ConversationID (stable). The Session Manager resolves this
// to an active SessionID (instance) before the message reaches the Agent Loop.
// The Agent Loop receives a *Session which has both ConversationID and ID (instance).

type OutboundMessage struct {
    ChannelID string          // which channel to send via (Session.ChannelID)
    UserID    string          // recipient (for DMs) or empty (for channel messages)
    Content   string
    ReplyToID string          // optional: reply to specific message
    Meta      map[string]any  // channel-specific formatting hints
}

// Routing: The Agent Loop has access to the Session, which contains ChannelID.
// When sending a response, it constructs OutboundMessage with session.ChannelID.
// The Channel.Send() method uses ChannelID to route to the correct destination.

type Channel interface {
    ID() string
    Listen(ctx context.Context, msgs chan<- InboundMessage) error
    Send(ctx context.Context, msg OutboundMessage) error
    React(ctx context.Context, messageID string, emoji string) error
}
```

---

### Memory (`internal/memory/memory.go`)

Top-level interface wrapping all four layers. Each layer also has its own interface
defined in its sub-package.

```go
type Memory interface {
    Episodic() EpisodicStore
    Semantic() SemanticStore
    Intent()   IntentStore
    Identity() IdentityStore
}
```

**Episodic** (`internal/memory/episodic/`)
```go
type Episode struct {
    ID        string
    SessionID string
    Role      string          // "user" | "assistant" | "tool"
    Content   string
    Timestamp time.Time
    Tags      []string
}

type EpisodicStore interface {
    Append(ctx context.Context, ep Episode) error
    Recent(ctx context.Context, sessionID string, n int) ([]Episode, error)
    Since(ctx context.Context, t time.Time) ([]Episode, error)
}
```

**Semantic** (`internal/memory/semantic/`)
```go
type MemoryEntry struct {
    ID         string
    Content    string
    Embedding  []float32
    Source     string          // "conversation" | "event" | "observation"
    Timestamp  time.Time
    Importance float32         // 0.0–1.0, set at insert time via heuristics
    Score      float32         // populated on retrieval (combined ranking), 0 on storage
}

type SemanticStore interface {
    Store(ctx context.Context, entry MemoryEntry) error
    StoreBatch(ctx context.Context, entries []MemoryEntry) error  // batches Embed() call
    Query(ctx context.Context, embedding []float32, topN int) ([]MemoryEntry, error)
    QueryText(ctx context.Context, text string, topN int) ([]MemoryEntry, error) // embeds internally
}

// StoreBatch optimization: when the agent loop stores multiple memories at end
// of turn, use StoreBatch to call EmbeddingProvider.Embed() once for all entries
// rather than N separate calls.
```

**Intent** (`internal/memory/intent/`)
```go
type IntentStatus string

const (
    IntentActive    IntentStatus = "active"
    IntentPaused    IntentStatus = "paused"
    IntentCompleted IntentStatus = "completed"
)

type Intent struct {
    ID          string
    Description string
    Condition   string         // natural language or structured rule
    Action      string         // what to do when condition is met
    Status      IntentStatus
    CreatedAt   time.Time
    LastChecked time.Time
    NextCheck   time.Time
}

type IntentStore interface {
    Create(ctx context.Context, intent Intent) error
    Update(ctx context.Context, intent Intent) error
    Active(ctx context.Context) ([]Intent, error)
    Due(ctx context.Context) ([]Intent, error)   // NextCheck <= now
    Complete(ctx context.Context, id string) error
}
```

**Identity** (`internal/memory/identity/`)
```go
type AgentProfile struct {
    Name        string
    Persona     string         // brief character description
    Traits      []string       // e.g. ["direct", "slightly sarcastic", "helpful"]
    Capabilities []string      // what the agent can do
}

type UserProfile struct {
    ID          string
    Name        string
    Timezone    string
    Preferences map[string]string
    Notes       string
}

type RelationshipState struct {
    TrustLevel      int            // 1-5
    CommunicationStyle string      // "concise" | "detailed" | "casual"
    OngoingContext  []string       // active threads, current projects
    LastInteraction time.Time
}

// v1 note: RelationshipState is included in context for LLM interpretation,
// NOT used for runtime branching. The LLM decides what "trust level 3" means
// based on its understanding. This is intentional — explicit runtime rules
// (e.g., "trust < 3 requires confirmation") can be added in v2 if the
// LLM-interpreted approach proves insufficient. The typed struct still
// provides queryability and persistence advantages over flat text.

type IdentityStore interface {
    Agent() (AgentProfile, error)
    SetAgent(ctx context.Context, profile AgentProfile) error
    User() (UserProfile, error)
    SetUser(ctx context.Context, profile UserProfile) error
    Relationship() (RelationshipState, error)
    UpdateRelationship(ctx context.Context, state RelationshipState) error
}
```

---

### Context Budget Manager (`internal/budget/budget.go`)

Assembles the optimal context window for each LLM call within a token budget.

```go
type ContextCandidate struct {
    Role      string          // "system" | "user" | "assistant" | "memory" | "identity"
    Content   string
    Priority  float32         // 0.0–1.0; higher = more important to include
    Recency   time.Time       // more recent = higher priority when scores tie
    Tokens    int             // pre-counted
}

type ContextBudget interface {
    Assemble(
        ctx      context.Context,
        budget   int,                    // max tokens
        fixed    []ContextCandidate,     // always included (identity, recent messages)
        ranked   []ContextCandidate,     // included in priority order until budget exhausted
    ) ([]Message, error)
}
```

---

### Scheduler (`internal/scheduler/scheduler.go`)

Proactive intent engine. Runs independently of inbound messages.

```go
type ScheduledTurn struct {
    IntentID  string
    Prompt    string          // injected into agent loop as if it were a user message
    SessionID string          // which session context to use; empty = new background session
}

type Scheduler interface {
    Start(ctx context.Context) error
    Stop() error
    // Scheduler reads IntentStore.Due() on its tick interval,
    // evaluates conditions, and emits ScheduledTurns to the agent loop.
    Turns() <-chan ScheduledTurn
}
```

---

### Event Bus (`internal/events/bus.go`)

Internal pub/sub. MQTT bridge publishes to this; agent loop and scheduler subscribe.

```go
type Event struct {
    Topic     string
    Payload   []byte
    Source    string          // "mqtt" | "internal" | "scheduler"
    Timestamp time.Time
}

type Handler func(ctx context.Context, event Event) error

type EventBus interface {
    Publish(ctx context.Context, event Event) error
    Subscribe(topic string, handler Handler) (unsubscribe func())
    // Topic matching supports wildcards: "homelab/#" matches all homelab topics
}
```

---

### Action Log (`internal/actionlog/log.go`)

Audit trail for human oversight. Captures every decision/action for later review.

```go
type ActionType string

const (
    ActionToolCall    ActionType = "tool_call"
    ActionMessageSent ActionType = "message_sent"
    ActionScheduled   ActionType = "scheduled"
    ActionConfirm     ActionType = "confirmation"
    ActionError       ActionType = "error"
)

type ActionEntry struct {
    ID        string
    Timestamp time.Time
    Type      ActionType
    Summary   string            // human-readable one-liner
    Details   map[string]any    // full context, serialized as JSON
    SessionID string
    ToolName  string            // if applicable
    Success   *bool             // nil for non-tool actions
}

type ActionRollup struct {
    ID          string
    Period      string            // "hour" | "day" | "week"
    StartTime   time.Time
    EndTime     time.Time
    Summary     string            // narrative summary
    ActionCount int
    ErrorCount  int
    TopTools    []ToolCount       // sorted by count desc
}

type ToolCount struct {
    Name  string
    Count int
}

type ActionLog interface {
    Record(ctx context.Context, entry ActionEntry) error
    Query(ctx context.Context, since, until time.Time, types []ActionType) ([]ActionEntry, error)
    Recent(ctx context.Context, n int) ([]ActionEntry, error)
    GetRollup(ctx context.Context, period string, t time.Time) (ActionRollup, error)
    GenerateRollups(ctx context.Context) error  // called by scheduler
}
```

**Recording points** (instrument these in v1):
- Tool executor: after every tool call (success or failure)
- Channel: after every outbound message
- Scheduler: when a scheduled turn fires
- Confirmation gate: when confirmation requested, approved, rejected, expired
- Agent loop: on any error that surfaces to user

**Rollup generation:**
- Scheduler calls `GenerateRollups()` hourly
- Hourly rollups: **template only** (counts by type, top tools) — no LLM calls
- Daily rollups: template + optional LLM narrative summary (1 call/day)
- Weekly rollups: template + optional LLM narrative summary (1 call/week)
- Rollups are idempotent — regenerating for the same period overwrites

**Cost note:** LLM summarization is disabled by default for rollups. Enable via
`[actionlog] llm_summaries = true`. When enabled, expect ~1-2 LLM calls/day for
narrative summaries. Hourly rollups never use LLM to avoid cost accumulation.

---

### Session Manager (`internal/agent/session.go`)

Manages session lifecycle: creation, resumption, timeout, cleanup.

```go
type SessionManager interface {
    // GetOrCreate returns existing session if active, creates new if expired/missing
    // Uses 30-minute inactivity window for session expiry
    GetOrCreate(ctx context.Context, conversationID string, channelID string, userID string) (*Session, error)
    
    // Get returns session by ID, nil if not found
    Get(sessionID string) *Session
    
    // Touch updates last_active timestamp
    Touch(sessionID string) error
    
    // Close ends a session, persists final state
    Close(sessionID string) error
    
    // ActiveCount returns number of currently active sessions
    ActiveCount() int
    
    // SetMaxConcurrent limits concurrent sessions (0 = unlimited)
    SetMaxConcurrent(n int)
}
```

Session timeout logic:
- Session expires after 30 minutes of inactivity (configurable)
- On new message: check if last session for this conversation_id is within window
- If yes: resume that session (same session instance ID)
- If no: create new session instance (new ULID), same conversation_id
- Expired sessions are cleaned up by a background goroutine every 5 minutes

---

### Agent Loop (`internal/agent/loop.go`)

The central reasoning cycle. Coordinates all other components.

```go
type LoopConfig struct {
    Provider      Provider
    Memory        Memory
    Budget        ContextBudget
    Registry      Registry        // tool registry
    MaxRounds     int             // max tool call rounds per turn (prevent infinite loops)
    MaxQueueDepth int             // max pending turns before backpressure (default: 20)
}

// Backpressure: The Agent Loop maintains an input queue for all turn sources
// (inbound messages, scheduled turns, event-triggered intents). If queue depth
// exceeds MaxQueueDepth:
// - New scheduled turns are dropped with WARN log
// - Inbound messages return "I'm currently overloaded, please try again shortly"
// - Events creating intents set NextCheck to now+5min instead of immediate
// This prevents unbounded goroutine growth under load.

type AgentLoop interface {
    // Run processes one inbound message through the full think → act → remember cycle.
    // Returns the final assistant response.
    Run(ctx context.Context, session *Session, msg InboundMessage) (string, error)

    // RunScheduled processes a proactive turn injected by the Scheduler.
    RunScheduled(ctx context.Context, turn ScheduledTurn) error
}
```

## 6. Data Models

All persistent data lives in a single SQLite database (`chandra.db`). Each memory layer
owns its tables. Migrations are managed via `store/migrations/` using sequential
numbered SQL files. The runtime applies pending migrations on startup.

---

### Schema Overview

```sql
-- Episodic memory
CREATE TABLE episodes (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL,
    role        TEXT NOT NULL,         -- user | assistant | tool
    content     TEXT NOT NULL,
    timestamp   INTEGER NOT NULL,      -- unix ms
    tags        TEXT                   -- JSON array
);
CREATE INDEX idx_episodes_session ON episodes(session_id, timestamp DESC);
CREATE INDEX idx_episodes_timestamp ON episodes(timestamp DESC);

-- Semantic memory
-- Metadata stored in standard table; embeddings in sqlite-vec virtual table.
-- Joined at query time by shared id.
CREATE TABLE memory_entries (
    id          TEXT PRIMARY KEY,
    content     TEXT NOT NULL,
    source      TEXT NOT NULL,         -- conversation | event | observation
    timestamp   INTEGER NOT NULL,
    importance  REAL DEFAULT 0.5       -- 0.0–1.0, updated by runtime over time
);
CREATE INDEX idx_memory_timestamp ON memory_entries(timestamp DESC);

-- sqlite-vec virtual table
-- Dimension MUST match the configured embedding model:
--   text-embedding-3-small: 1536
--   text-embedding-3-large: 3072
--   nomic-embed-text (Ollama): 768
-- Changing embedding models requires a migration to recreate this table.
CREATE VIRTUAL TABLE memory_embeddings USING vec0(
    id          TEXT PRIMARY KEY,
    embedding   FLOAT[1536]  -- matches text-embedding-3-small default
);

-- Intent store
CREATE TABLE intents (
    id           TEXT PRIMARY KEY,
    description  TEXT NOT NULL,
    condition    TEXT NOT NULL,
    action       TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'active',
    created_at   INTEGER NOT NULL,
    last_checked INTEGER,
    next_check   INTEGER
);
CREATE INDEX idx_intents_status ON intents(status);
CREATE INDEX idx_intents_next_check ON intents(next_check);

-- Agent identity (single row, keyed by name)
CREATE TABLE agent_profile (
    id           TEXT PRIMARY KEY DEFAULT 'chandra',
    name         TEXT NOT NULL,
    persona      TEXT NOT NULL,
    traits       TEXT NOT NULL,        -- JSON array
    capabilities TEXT NOT NULL         -- JSON array
);

-- User profile (single row in v1, keyed by user id)
CREATE TABLE user_profile (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    timezone     TEXT NOT NULL,
    preferences  TEXT,                 -- JSON object
    notes        TEXT
);

-- Relationship state (one row per agent+user pair)
CREATE TABLE relationship_state (
    agent_id           TEXT NOT NULL,
    user_id            TEXT NOT NULL,
    trust_level        INTEGER NOT NULL DEFAULT 3,
    communication_style TEXT NOT NULL DEFAULT 'concise',
    ongoing_context    TEXT,           -- JSON array of strings
    last_interaction   INTEGER,
    PRIMARY KEY (agent_id, user_id)
);

-- Tool telemetry
CREATE TABLE tool_telemetry (
    id           TEXT PRIMARY KEY,
    tool_name    TEXT NOT NULL,
    called_at    INTEGER NOT NULL,
    latency_ms   INTEGER NOT NULL,
    success      INTEGER NOT NULL,     -- 0 | 1
    error        TEXT,
    retries      INTEGER DEFAULT 0
);
CREATE INDEX idx_telemetry_tool ON tool_telemetry(tool_name, called_at DESC);

-- Sessions (lightweight, for context continuity)
-- conversation_id is stable (channel+user), session instances track activity windows
CREATE TABLE sessions (
    id              TEXT PRIMARY KEY,      -- ULID, unique per session instance
    conversation_id TEXT NOT NULL,         -- stable: hash(channel_id + user_id)
    channel_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    started_at      INTEGER NOT NULL,
    last_active     INTEGER NOT NULL,
    meta            TEXT                   -- JSON, channel-specific context
);
CREATE INDEX idx_sessions_conversation ON sessions(conversation_id, last_active DESC);
CREATE INDEX idx_sessions_channel ON sessions(channel_id, last_active DESC);

-- Action log (audit trail for human review)
-- Designed to power a future web console; v1 uses CLI access
CREATE TABLE action_log (
    id           TEXT PRIMARY KEY,
    timestamp    INTEGER NOT NULL,
    type         TEXT NOT NULL,        -- tool_call | message_sent | scheduled | confirmation | error
    summary      TEXT NOT NULL,        -- human-readable one-liner
    details      TEXT,                 -- JSON: full parameters, results, context
    session_id   TEXT,
    tool_name    TEXT,
    success      INTEGER               -- 1 | 0 | NULL (for non-tool actions)
);
CREATE INDEX idx_action_log_time ON action_log(timestamp DESC);
CREATE INDEX idx_action_log_type ON action_log(type, timestamp DESC);

-- Action rollups (hourly/daily/weekly summaries)
CREATE TABLE action_rollups (
    id           TEXT PRIMARY KEY,
    period       TEXT NOT NULL,        -- hour | day | week
    start_time   INTEGER NOT NULL,
    end_time     INTEGER NOT NULL,
    summary      TEXT NOT NULL,        -- templated or LLM-generated narrative
    action_count INTEGER NOT NULL,
    error_count  INTEGER NOT NULL,
    top_tools    TEXT                  -- JSON: [{"name": "...", "count": N}, ...]
);
CREATE INDEX idx_rollups_period ON action_rollups(period, start_time DESC);

-- Confirmation queue (Tier 4 actions awaiting user approval)
CREATE TABLE confirmations (
    id           TEXT PRIMARY KEY,
    session_id   TEXT NOT NULL,
    tool_name    TEXT NOT NULL,
    parameters   TEXT NOT NULL,        -- JSON
    description  TEXT NOT NULL,        -- human-readable summary
    requested_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    status       TEXT DEFAULT 'pending' -- pending | approved | rejected | expired
);
CREATE INDEX idx_confirmations_session ON confirmations(session_id, status);
```

---

### In-Memory Types (runtime only, not persisted)

```go
// Session holds live state for an active conversation.
// Persisted sessions table stores only lightweight metadata.
type Session struct {
    ID             string    // ULID, unique per session instance
    ConversationID string    // stable: hash(channel_id + user_id)
    ChannelID      string
    UserID         string
    StartedAt      time.Time
    LastActive     time.Time
    // Runtime state — not persisted
    cancelFn       context.CancelFunc
    msgChan        chan InboundMessage
}

// ToolReliability is computed at runtime from telemetry, not stored directly.
// Recomputed on startup and refreshed periodically from tool_telemetry table.
// Uses a 30-day window to avoid stale historical data dragging scores.
type ToolReliability struct {
    ToolName      string
    SuccessRate   float32     // 0.0–1.0 over last 100 calls within 30-day window
    P50LatencyMs  int
    P95LatencyMs  int
    LastError     string
    LastErrorAt   time.Time
    SampleSize    int         // number of calls in the window
}

// ContextWindow is the assembled input to a Provider.Complete() call.
// Produced by the Context Budget Manager each turn, never stored.
type ContextWindow struct {
    Messages    []Message
    Tools       []ToolDef
    TotalTokens int
    Dropped     int          // number of candidates dropped due to budget
}
```

---

### Embedding Strategy

Embeddings are generated via the configured provider's embeddings endpoint
(OpenAI-compatible: `POST /v1/embeddings`). The model is configurable; default
is `text-embedding-3-small` (1536 dimensions).

**Vector search: sqlite-vec**

Vector similarity search is handled by [sqlite-vec](https://github.com/asg017/sqlite-vec),
a SQLite extension that brings native vector operations into SQL queries. This gives us
the query richness of a dedicated vector store (filtered similarity search, metadata
combined with vector distance in a single query) without any additional infrastructure.

Everything stays in a single `chandra.db` file. One backup, zero new services to run.

Example query pattern (joins metadata table with embeddings virtual table):
```sql
SELECT m.id, m.content, m.source, m.timestamp, m.importance,
       vec_distance_cosine(e.embedding, ?) AS distance
FROM memory_embeddings e
JOIN memory_entries m ON m.id = e.id
WHERE m.timestamp > ?            -- metadata filter combined with vector search
  AND m.source = 'conversation'
ORDER BY distance ASC
LIMIT 10;
```

The `SemanticStore` interface is unaffected by this choice — the implementation
uses sqlite-vec SQL functions rather than Go-side cosine math, but callers see
no difference. Migration to a dedicated vector store remains possible in future
without interface changes, but is not anticipated to be necessary.

**Rationale for sqlite-vec over a dedicated vector store:**
A separate vector database (Chroma, Qdrant, Weaviate) solves a scale problem
we do not have and adds infrastructure that must be run, monitored, and backed
up independently. That is a poor trade for a single-user personal assistant.
sqlite-vec gives us equivalent query capability at zero operational cost.

**Scaling note:** sqlite-vec's default vec0 performs brute-force exact kNN via
full table scan. At 10k entries (~60MB of float32s), this is <10ms. At 100k+
entries, consider: (1) adding a time-window filter to queries (last 90 days),
(2) using sqlite-vec's experimental IVF indexing, or (3) periodic archival of
old low-importance memories. Monitor query latency as the store grows.

---

### ID Strategy

All IDs are ULIDs (Universally Unique Lexicographically Sortable Identifiers).
- Sortable by creation time without a separate timestamp index in most cases
- No coordination required (safe for future multi-process use)
- Human-readable enough for debugging
- Library: `github.com/oklog/ulid/v2`

## 7. Implementation Notes

Per-package guidance for implementors. These notes exist to prevent bad local
decisions during implementation. Read them before writing any code in the
relevant package.

---

### `cmd/chandrad` — Daemon Entrypoint

- Load config from `~/.config/chandra/config.toml` (fallback to `./chandra.toml`)
- Apply SQLite migrations from `store/migrations/` in sequence on startup
- Load sqlite-vec extension before any database operations (see `store/` notes)
- Start components in dependency order:
  1. Database + migrations
  2. Memory layers
  3. Tool registry (registers all built-in skills)
  4. Provider
  5. Event bus
  6. MQTT bridge (subscribes to event bus)
  7. Scheduler (reads intent store, starts tick loop)
  8. Channel listeners (Discord etc.)
  9. Session manager
- Handle SIGTERM/SIGINT with graceful shutdown in reverse order
- All components receive a root `context.Context`; cancellation propagates shutdown

**Health check (`chandra health`)** returns structured status:
```json
{
  "status": "healthy|degraded|unhealthy",
  "uptime_seconds": 3600,
  "components": {
    "database": {"status": "ok", "latency_ms": 2},
    "discord": {"status": "ok", "connected": true},
    "mqtt": {"status": "degraded", "connected": false, "reconnecting": true},
    "scheduler": {"status": "ok", "pending_intents": 3},
    "provider": {"status": "ok", "last_call_ms": 1250}
  },
  "active_sessions": 1,
  "memory_entries": 4523,
  "action_log_today": 47
}
```
Status is "healthy" if all components ok, "degraded" if non-critical components down
(MQTT), "unhealthy" if critical components down (database, provider).

### `cmd/chandra` — CLI Entrypoint

- Commands: `chandra start`, `chandra stop`, `chandra status`, `chandra health`, `chandra memory search <query>`,
  `chandra intent list`, `chandra intent add`, `chandra intent complete <id>`,
  `chandra tool list`, `chandra tool telemetry <name>`,
  `chandra log --today`, `chandra log --tail N`, `chandra log --day YYYY-MM-DD`,
  `chandra log --week`, `chandra log --drill <id>`
- Communicates with `chandrad` via Unix socket
- Socket path: `$XDG_RUNTIME_DIR/chandra/chandra.sock` (fallback: `/tmp/chandra-$UID/chandra.sock`)
- Socket directory created with mode 0700; socket created with mode 0600
- On startup, remove stale socket if process that created it is dead (check via flock or pid file)
- JSON protocol over the socket — request/response, not streaming

---

### `store/` — Database + Migrations

**sqlite-vec extension loading:**
```go
import "github.com/mattn/go-sqlite3"
import "github.com/asg017/sqlite-vec-go-bindings/cgo"

func init() {
    sqlite3.Auto(vec.Auto) // registers sqlite-vec before any connection opens
}
```

This must happen in an `init()` function in the store package before any
`sql.Open()` call. Claude Code: do not skip this — queries against
`memory_embeddings` will silently fail without it.

- Use `github.com/golang-migrate/migrate/v4` for migration management
- Migration files: `store/migrations/001_initial.up.sql`, `001_initial.down.sql`, etc.
- Always run migrations in a transaction; roll back on error
- Never modify an existing migration file — add a new one
- Down migrations are required for development rollback capability
- Production deployments may skip down migrations but dev workflow needs them
- Enable WAL mode on startup: `PRAGMA journal_mode=WAL`
- Enable foreign keys: `PRAGMA foreign_keys=ON`
- Set busy timeout: `PRAGMA busy_timeout=5000`

---

### `internal/memory/semantic/` — Semantic Store

- Insert: write to `memory_entries` first, then `memory_embeddings` in same transaction
- Query pattern (always join both tables):
```sql
SELECT m.id, m.content, m.source, m.timestamp, m.importance,
       vec_distance_cosine(e.embedding, ?) AS distance
FROM memory_embeddings e
JOIN memory_entries m ON m.id = e.id
WHERE m.timestamp > ?
ORDER BY distance ASC
LIMIT ?;
```
- **Distance to Score mapping:** The query returns `distance` (0.0 = identical, 2.0 = opposite).
  Convert to `Score` before returning: `Score = 1.0 - (distance / 2.0)` which gives 1.0 for
  identical, 0.0 for opposite. The CBM formula expects Score in 0.0–1.0 range.
- Embed via EmbeddingProvider before storing — `SemanticStore` constructor takes
  an `EmbeddingProvider` instance (configured from `[embeddings]` config section).
  This is typically a different service than the chat provider (e.g., OpenAI
  embeddings + Claude chat).
- **Dimension validation:** On startup, verify `EmbeddingProvider.Dimensions()`
  matches the `memory_embeddings` table dimension. If mismatch, fail with clear
  error: "Embedding model dimension (3072) does not match schema (1536). Run
  migration or change embedding model." Prevents silent corruption.
- `QueryText()` embeds the query string then calls `Query()` — do not duplicate logic
- `importance` field: computed at insert time using the following v1 heuristics:
  - Turns containing tool calls: 0.6 (action occurred)
  - Turns > 200 tokens: 0.6 (substantive exchange)
  - Explicit user reinforcement ("remember this", "important"): 0.8
  - Default: 0.5
  - Future: decay over time, boost on retrieval (spaced repetition)

---

### `internal/memory/episodic/` — Episodic Store

- Append is append-only. Never update or delete episodes.
- Tags are stored as JSON array string: `["tool_call", "homeassistant"]`
- `Recent()` returns newest-first. Callers reverse if needed.
- Keep a per-session episode count in memory (not DB) for fast limit checks

---

### `internal/memory/intent/` — Intent Store

- `Due()` query: `WHERE status = 'active' AND next_check <= unixepoch() * 1000`
- After the Scheduler processes an intent, always call `Update()` to set
  `last_checked` and `next_check` — never leave an intent in a stale state
- `next_check` for recurring intents: set by the Scheduler based on condition
  evaluation. For simple time-based intents, parse the condition string.
  For complex conditions, re-check every configured interval (default: 5 min)

---

### `internal/memory/identity/` — Identity Store

- Agent profile: upsert on `id = 'chandra'`. Only one row ever exists.
- User profile: v1 has one user. Key on user ID from channel metadata.
- Relationship state: loaded into memory on session start, written back after
  each session ends. Do not write on every turn — batch the update.
- `OngoingContext` array: keep max 20 items. Oldest dropped when limit reached.

---

### `internal/budget/` — Context Budget Manager

- Token counting: use a **local tokenizer**, not HTTP API calls.
  `Provider.CountTokens()` must use a local library (e.g., `tiktoken-go` for
  OpenAI models, or a compatible tokenizer for Claude). Making HTTP calls to
  count tokens for every candidate would violate the <5s latency requirement.
  Do not estimate with character counts or word counts — use actual tokenization.

**Ranking formula for candidates:**

```
score = (0.4 × semantic_similarity) + (0.3 × recency_score) + (0.3 × importance)

where:
  semantic_similarity = 1.0 - cosine_distance     // already 0.0–1.0
  recency_score = max(0, 1.0 - (hours_ago / 168)) // decays to 0 over 7 days
  importance = stored importance field            // 0.0–1.0
```

Weights are configurable via `[budget]` config section. The formula ensures:
- Recent, semantically relevant memories rank highest
- Old but important memories can still surface
- Completely stale content (>7 days, low importance, low similarity) drops out

- Assembly algorithm:
  1. Add all `fixed` candidates unconditionally
  2. If fixed candidates alone exceed budget: truncate the oldest messages from
     the "recent messages" fixed set until under budget. Log at WARN. If still
     over budget after removing all but the current turn, truncate the current
     user message itself (keep first N tokens). Never panic — graceful degradation.
  3. Compute score for all `ranked` candidates using formula above
  4. Sort by score DESC (ties broken by recency)
  5. Greedily add ranked candidates until budget exhausted
  6. Log number of dropped candidates at DEBUG level
- The system prompt (identity + persona) is always a fixed candidate
- Recent messages (last N turns) are always fixed candidates
- Retrieved semantic memories are ranked candidates
- Tool descriptions count against the budget — include only tools relevant
  to the current session context where possible

---

### `internal/tools/executor.go` — Tool Executor

- All tool calls from a single LLM response dispatch in parallel goroutines
- Each call gets its own `context.Context` with a per-tool timeout (default: 30s,
  configurable per tool via `ToolDef`)
- Retry logic: on transient errors (network, timeout), retry up to 2 times
  with exponential backoff. Do not retry on logic errors (bad parameters etc.)
- Write a `tool_telemetry` row after every execution — success or failure
- Tier enforcement happens here before dispatch:
  ```go
  if err := registry.EnforceCapabilities(call); err != nil {
      return ToolResult{Error: err}, nil  // surface to LLM, don't panic
  }
  ```
- **Confirmations cleanup:** The Scheduler runs a cleanup task every tick that:
  - Marks pending confirmations with `expires_at < now` as status='expired'
  - On approval attempt, re-validates `expires_at` — reject if expired even if status is pending
  - Prunes confirmations older than 7 days (any status) to prevent table growth

- Tier 4 (confirm:required): the confirmation flow is asynchronous:
  1. Write a `confirmations` row with status='pending'
  2. Return immediately to the LLM with: "Action requires confirmation. 
     Confirmation ID: {id}. Waiting for user approval."
  3. The agent loop completes normally — no goroutine sits waiting
  4. User approves via CLI (`chandra confirm {id}`) or channel reaction
  5. Approval updates the row to status='approved'
  6. On next relevant agent turn (or via Scheduler polling confirmations table),
     the runtime detects the approved action and executes it
  7. Result is delivered to the user as a new message, not as a continuation
     of the original turn
  
  This avoids tying up executor state. The tradeoff: confirmed actions execute
  on a subsequent turn, not inline. The original conversational context may
  have moved on. For v1 this is acceptable; inline confirmation with suspended
  execution state is a v2 consideration.

---

### `internal/scheduler/` — Scheduler

- Runs in its own goroutine, started by `chandrad` after memory is ready
- Tick interval: configurable, default 60 seconds
- **Important:** The scheduler does NOT evaluate all intents on each tick.
  `IntentStore.Due()` uses an indexed query (`WHERE next_check <= now`) to
  return only intents that are actually due. This is O(due) not O(all).
  A large intent store with few due items remains cheap to tick.
- On each tick:
  1. Call `IntentStore.Due()` (indexed, returns only due intents) 
  2. For each due intent, evaluate condition (natural language intents: ask LLM;
     structured intents: evaluate locally)
  3. If condition met: emit `ScheduledTurn` to `Turns()` channel, update intent
  4. If condition not met: update `last_checked`, set `next_check`, continue
- The `Turns()` channel is buffered (size 10). If full, log and skip — do not block.
- Scheduler and Scheduler-injected turns share the same Agent Loop as inbound
  messages. Session ID for scheduled turns: use a dedicated background session,
  not an active user session, unless the intent specifies otherwise.

---

### `internal/events/` — Event Bus + MQTT Bridge

- Internal bus: simple topic-based pub/sub using Go channels and sync.Map
- Topic matching: support `#` wildcard (matches any suffix) and `+` (single segment)
  consistent with MQTT conventions

**Event-to-Agent path:** Events that should trigger agent reasoning follow this flow:
1. Event arrives on bus (from MQTT bridge or internal publisher)
2. A registered handler evaluates if the event warrants agent attention
3. If yes, handler creates an Intent with `condition: "immediate"` and `action: <event summary>`
4. Scheduler picks up the intent on next tick (or immediately if using intent.NextCheck = now)
5. Scheduler emits ScheduledTurn to Agent Loop via Turns() channel

Events do NOT call RunScheduled directly — they go through the Intent store.
This ensures all agent triggers are logged, persist across restarts, and can be deduplicated.
- **MQTT is enabled by default** with embedded broker on localhost (`127.0.0.1:1883`).
  This enables the event-driven architecture out of the box with zero external
  infrastructure. Set `mode = "disabled"` to explicitly turn it off.
  
  - `mode = "embedded"` (default): Chandra runs its own broker, localhost only
  - `mode = "external"`: Connect to existing broker (homelab infrastructure)
  - `mode = "disabled"`: No MQTT, internal event bus still works for scheduler/confirmations

- **Embedded broker option:** For self-contained deployments without external MQTT:
  ```toml
  [mqtt]
  mode = "embedded"
  bind = "127.0.0.1:1883"  # localhost only
  ```
  Uses `github.com/mochi-mqtt/server` (~3k lines, pure Go, embeddable)
  
  **Embedded broker security:**
  - Localhost binding (`127.0.0.1`): no auth required, machine boundary is security
  - Network binding (`0.0.0.0` or specific IP): **refuse to start** without auth configured
  - Auth config: `username`, `password` (env var interpolation supported)
  - TLS config: `tls_cert`, `tls_key` (recommended for network exposure)
  - mochi-mqtt supports ACLs if finer-grained topic permissions are needed

- **External broker:** Connect using `github.com/eclipse/paho.mqtt.golang`
  ```toml
  [mqtt]
  mode = "external"
  broker = "mqtt://10.1.0.194:1883"
  ```
  - Subscribe to configured topic patterns on connect
  - On message: publish to internal bus with `Source: "mqtt"`
  - Reconnect on disconnect with exponential backoff:
    - Initial delay: 1s, max delay: 5min, max retries: unlimited
    - Connection status exposed via health check
    - If disconnected, MQTT-dependent intents are skipped (not failed) until reconnected
- Handlers are dispatched to a bounded worker pool (default: 10 workers, configurable)
- Events queue into a buffered channel (default: 500 capacity — sized for burst scenarios
  like network equipment rebooting and broadcasting many state changes)
- If queue is full, apply topic-priority shedding:
  - High-priority topics (configurable, e.g., "security/#", "alert/#") are never dropped
  - Low-priority topics (e.g., "sensor/#", "debug/#") are dropped first
  - Log all drops at WARN with topic name
- This prevents unbounded goroutine growth while protecting important events
- Workers never block the bus; slow handlers don't starve others

---

### `internal/channels/discord/` — Discord Channel

**Prompt injection awareness:** User messages from Discord are untrusted input.
The Agent Loop should:
- Never execute tool calls that appear verbatim in user input (e.g., user says
  "run homeassistant.turn_off('all')" — the LLM may comply)
- Consider a tool allowlist per channel (DMs get fewer tools than owner channels)
- Log and flag suspicious patterns (e.g., "ignore previous instructions")
- For v1: document the risk, implement allowlist, defer advanced defenses to v2

- Use `github.com/bwmarrin/discordgo`
- Listen for `MessageCreate` events; filter to configured channel IDs
- Conversation ID: stable hash of Discord channel ID + user ID (persists forever)
- Session instance: new ULID created when last_active > 30 minutes ago
- Resume: if last session instance for this conversation_id has last_active < 30 min,
  reuse that session instance. Otherwise create new session instance with same conversation_id.
- This means: conversation_id groups all sessions for a user+channel pair;
  session instance ID tracks individual activity windows for context loading.
- `React()`: use `discordgo.Session.MessageReactionAdd()`
- Rate limiting: discordgo handles this automatically; do not implement separately
- On startup: set bot status to Online

---

### `internal/agent/loop.go` — Agent Loop

The most critical package. The think → act → remember cycle:

```
func (l *AgentLoop) Run(ctx, session, msg):
  1. Load session context (recent episodes, identity, relationship)
  2. Retrieve semantic memories relevant to msg.Content (top 5)
  3. Assemble context window via ContextBudget.Assemble()
  4. Call Provider.Complete()
  5. If response.ToolCalls non-empty:
       a. Dispatch all tool calls via Executor (parallel)
       b. Append tool results to message history
       c. Call Provider.Complete() again (tool result round)
       d. Repeat up to MaxRounds
  6. Append final exchange to EpisodicStore
  7. Store exchange in SemanticStore (embed + index)
  8. Update RelationshipState.LastInteraction
  9. Return final assistant message content
```

- `MaxRounds` default: 5 (configurable via `[agent] max_tool_rounds` in config).
  Prevents infinite tool call loops. For complex multi-tool workflows, increase
  to 10+. Can also be overridden per-intent for known deep chains.
- If MaxRounds exceeded: return a graceful "I wasn't able to complete that"
  message rather than an error. Log at WARN level with full chain trace.
- Step 7 (semantic storage): do not embed every single turn. Evaluation order:
  1. Check for explicit user reinforcement ("remember this", "important") → store at 0.8
  2. Skip pure tool-call-only turns (no user/assistant content)
  3. Skip turns shorter than 50 tokens (unless caught by step 1)
  4. Store remaining substantive exchanges
  The reinforcement check MUST happen before the length filter — a short
  "remember: never deploy on Fridays" must be stored despite being < 50 tokens.
- Step 8: update relationship state after every run but write to DB only on
  session end (see identity notes above).

---

### `internal/config/` — Configuration

TOML format. Example structure:
```toml
[agent]
name = "Chandra"
persona = "Direct, helpful, slightly sarcastic personal assistant — I am Chandra."
max_tool_rounds = 5  # increase for complex multi-tool workflows

[budget]
semantic_weight = 0.4
recency_weight = 0.3
importance_weight = 0.3
recency_decay_hours = 168  # 7 days

[provider]
# Chat completions provider
base_url = "https://api.anthropic.com"  # note: no /v1 suffix for Anthropic
api_key  = "${CHANDRA_API_KEY}"
model    = "claude-sonnet-4-6"
type     = "anthropic"                  # anthropic | openai | ollama

[embeddings]
# Embeddings provider (often different from chat — Anthropic doesn't serve embeddings)
base_url = "https://api.openai.com/v1"  # or http://localhost:11434/v1 for Ollama
api_key  = "${CHANDRA_EMBEDDINGS_KEY}"  # can be same or different key
model    = "text-embedding-3-small"

[database]
path = "~/.local/share/chandra/chandra.db"

[scheduler]
tick_interval = "60s"

# MQTT is ENABLED BY DEFAULT with embedded broker on localhost.
# This enables the event-driven architecture out of the box.
[mqtt]
mode     = "embedded"                 # embedded (default) | external | disabled
bind     = "127.0.0.1:1883"           # localhost only, no auth needed
topics   = ["chandra/#"]              # internal event topics

# To connect to existing broker instead:
# [mqtt]
# mode   = "external"
# broker = "mqtt://mqtt.lacy.casa:1883"
# topics = ["homelab/#"]

# Alternative: embedded broker (no external infrastructure needed)
# [mqtt]
# mode = "embedded"
# bind = "127.0.0.1:1883"     # localhost only — no auth needed
# topics = ["homelab/#"]

# Embedded broker with network exposure (requires auth):
# [mqtt]
# mode = "embedded"
# bind = "0.0.0.0:1883"       # network exposed — auth REQUIRED
# username = "chandra"
# password = "${MQTT_PASSWORD}"
# tls_cert = "/path/to/cert.pem"  # optional but recommended
# tls_key = "/path/to/key.pem"
# topics = ["homelab/#"]

# Alternative: explicitly disabled (same as omitting section)
# [mqtt]
# mode = "disabled"

[channels.discord]
token      = "${CHANDRA_DISCORD_TOKEN}"
channel_ids = ["1464891055584444501"]

[tools]
confirmation_timeout = "24h"
default_tool_timeout = "30s"
```

- Environment variable interpolation: `${VAR_NAME}` in string values
- Never log config values that may contain secrets
- Validate all required fields on startup; fail fast with clear error messages
### Config Safety

The agent can reason about and modify its own config (no web console in v1). This
creates a risk: bad config changes can prevent startup, requiring SSH intervention.

**Prevention layers:**

1. **Validate before write:** Config tool parses new config, checks required fields,
   verifies env vars exist, validates URLs/endpoints before writing anything.

2. **Backup before change:** Before any modification, copy current config to
   `config.toml.backup.N` (keep last 3). Track "last known good" config separately.

3. **Atomic writes:** Write to temp file, validate, atomic rename. Never partial writes.

4. **Rollback on failed startup:**
   ```
   chandrad starts
     → load config
     → if startup fails within 10 seconds:
         → restore last known good config
         → restart
         → if still fails: enter safe mode
   ```

5. **Safe mode (`chandrad --safe`):**
   - Ignores config file entirely
   - Embedded MQTT on localhost only
   - Uses existing database (read-only for config recovery)
   - No external connections (Discord, external MQTT)
   - CLI still works for diagnostics and manual config repair
   - Agent can propose fixes, user approves via CLI

6. **Config changes are Tier 4:** Any config modification tool requires user
   confirmation before applying. Prevents accidental self-bricking.

**Config modification flow:**
```
Agent proposes config change
  → Requires user confirmation (Tier 4)
  → User approves via CLI or channel reaction
  → Config tool validates proposed change
  → If invalid: reject with detailed error
  → Backup current config
  → Write new config atomically
  → Restart chandrad with 10s health check timeout
  → If healthy: mark new config as "last known good"
  → If unhealthy: automatic rollback, alert user
```

**Recovery without SSH:**
If the agent is in safe mode, the user can still interact via CLI on the local machine.
For remote recovery without SSH, the design could add a webhook-based recovery endpoint
in v2, but v1 assumes local CLI access is available for emergency recovery.

- **File permissions:** On startup, `chandrad` must verify:
  - `~/.config/chandra/` directory is mode 0700 (or stricter)
  - `config.toml` is mode 0600 (or stricter)
  - Refuse to start if config is world-readable or group-readable
  - Create directories with correct permissions if they don't exist

## 8. Dependencies

All dependencies are Go modules unless noted. Pin to exact versions in `go.mod`.
No dependency should be added during implementation without a clear reason —
this list is the complete set for v1.

### Core Runtime

| Package | Purpose |
|---------|---------|
| `github.com/mattn/go-sqlite3` | SQLite driver (CGO) |
| `github.com/asg017/sqlite-vec-go-bindings/cgo` | sqlite-vec extension bindings |
| `github.com/golang-migrate/migrate/v4` | SQLite schema migrations |
| `github.com/oklog/ulid/v2` | ULID generation for all IDs |
| `github.com/BurntSushi/toml` | Config file parsing |

### Networking + Messaging

| Package | Purpose |
|---------|---------|
| `github.com/bwmarrin/discordgo` | Discord channel implementation |
| `github.com/eclipse/paho.mqtt.golang` | MQTT client (external broker mode) |
| `github.com/mochi-mqtt/server/v2` | Embedded MQTT broker (default, self-contained deployments) |

**mochi-mqtt operational impact:** The embedded broker adds ~3k lines of code,
one long-running goroutine, ~5-10MB memory overhead, and binds to a port (default
1883 on localhost). This is the cost of zero-config event-driven architecture.
If these resources are a concern, set `mode = "disabled"` or use an external broker.

### LLM + Embeddings

| Package | Purpose |
|---------|---------|
| `github.com/sashabaranov/go-openai` | OpenAI-compatible HTTP client (OpenAI, Ollama providers) |
| `github.com/anthropics/anthropic-sdk-go` | Native Anthropic client (Claude provider) |
| `github.com/pkoukk/tiktoken-go` | Local tokenizer for accurate token counting (no HTTP) |

Provider implementations:
- `openai/` — uses go-openai, works with OpenAI API and compatible endpoints (Ollama, vllm)
- `anthropic/` — uses anthropic-sdk-go, native Claude API support
- Both implement the same `Provider` interface

Embeddings are routed to a separate endpoint (config `[embeddings]` section) since
Anthropic doesn't serve embeddings. Typically OpenAI or a local Ollama instance.

Token counting uses local tokenization via tiktoken-go to avoid HTTP latency.

**Claude tokenizer note:** tiktoken-go implements OpenAI's tokenizers, not Claude's.
When using Anthropic as the provider, token counts are approximate (cl100k_base is
reasonably close). This is acceptable for budget purposes — the goal is staying
under limits, not exact billing. For precise Claude token counts, Anthropic offers
`/v1/messages/count_tokens` but the HTTP latency makes it unsuitable for per-candidate
counting in the CBM loop.

### CLI

| Package | Purpose |
|---------|---------|
| `github.com/spf13/cobra` | CLI command structure (`chandra` binary) |

### Observability + Utilities

| Package | Purpose |
|---------|---------|
| `log/slog` | Structured logging (stdlib, no external dependency) |
| `github.com/lmittmann/tint` | Human-readable slog handler for development |

Note: Using stdlib `slog` over `zap` for simplicity and stdlib alignment.
Production uses JSON handler; development uses tint for colored output.

### Testing

| Package | Purpose |
|---------|---------|
| `github.com/stretchr/testify` | Assertions and test suites |

### Notable Omissions (intentional)

- **No ORM** — raw `database/sql` with hand-written queries. sqlite-vec SQL
  is not compatible with ORMs anyway; consistency matters more than convenience.
- **No HTTP framework** — the Unix socket CLI protocol is simple enough for
  `net` + `encoding/json`. No gin, echo, fiber.
- **No DI framework** — constructor injection only. Wire/dig add complexity
  that isn't warranted at this scale.
- **No dedicated vector database** — embeddings stored in SQLite via sqlite-vec.

### CGO Note

`go-sqlite3` and `sqlite-vec-go-bindings` require CGO. This means:
- `CGO_ENABLED=1` for all builds
- A C compiler must be present in the build environment
- Cross-compilation requires a cross-compiler toolchain
- Docker build image must include `gcc` or `musl-gcc`

This is an accepted tradeoff. sqlite-vec does not have a pure-Go alternative.

---

## 9. Out of Scope / Future

These are explicitly deferred. Do not implement in v1. Document here so
future contributors understand these were deliberate decisions, not oversights.

### Deferred to v2+

**Multi-user support**
The identity layer assumes one user. `user_profile` has one row. Session
management does not enforce user isolation. Adding multi-user requires
scoping all memory layers by user ID and adding an auth layer. Significant
but not architecturally breaking — the interface shapes support it.

**Multi-agent orchestration**
Chandra is a single agent. Spawning sub-agents, delegating to specialist agents,
agent-to-agent communication — all deferred. The Scheduler and Intent store
are the seeds of this capability when the time comes.

**Event streaming infrastructure (Redpanda, Kafka)**
The v1 action log uses SQLite, which is sufficient for single-user throughput
(hundreds of actions/day). A dedicated event streaming platform would add value if:
- Multi-agent orchestration requires agents to publish/subscribe to each other
- Real-time streaming to external consumers (dashboards, alerts, SIEM)
- Replay/reprocessing of historical events at scale becomes necessary
- Throughput exceeds what SQLite can handle

Redpanda (Kafka-compatible, single binary, no JVM) would be a natural choice
if this need arises. For v1, the operational simplicity of SQLite wins.

**Web UI / Action Console**
No browser interface in v1. The `chandra` CLI is the management surface.
A future web console would provide:
- Action log timeline with filtering and drill-down
- Rollup visualizations (actions/hour, errors/day charts)
- Review queue for flagged or failed actions
- Real-time activity stream

The v1 action log schema and CLI are designed to power this console.
The console would talk to `chandrad` via the same Unix socket protocol,
extended with streaming support.

**Horizontal scaling / clustering**
Single process, single machine. SQLite's WAL mode handles concurrent
reads well but `chandrad` is not designed for multi-instance deployment.
Migration to Postgres + a proper vector store would be the path if scale
required it.

**Local model inference**
Not Chandra's responsibility. Use Ollama, LM Studio, or vllm and point
`provider.base_url` at their OpenAI-compatible endpoint. Chandra treats
local and cloud models identically.

**Plugin marketplace / external distribution**
No plugin registry, no install command, no versioning for external plugins
in v1. Skills are compiled in or loaded from a configured local directory.

**Voice interface**
No STT/TTS in v1. Future: a channel implementation that wraps a voice
pipeline would slot in cleanly without core changes.

**Additional channels**
Telegram, Signal, WhatsApp, IRC — all feasible as `Channel` interface
implementations. Discord is v1. Others are straightforward additions
that don't require core changes.

**Memory importance decay**
The `importance` field exists in the schema but is not actively managed in v1.
Default 0.5 on insert. Future: decay over time, boost on retrieval (spaced
repetition style), decay unused intents.

**Streaming responses**
`Provider.Complete()` is request/response in v1. `CompletionRequest.Stream`
field is reserved but not implemented. Streaming requires channel
implementations to support partial message updates (Discord does via
message edits). Deferred until channel layer matures.

---

## 10. Done Criteria

Chandra v1 is complete when all of the following are true.

### Functional

- [ ] `chandrad` starts cleanly, applies migrations, loads sqlite-vec, connects
      to Discord and MQTT without errors
- [ ] Inbound Discord message triggers a full agent loop turn and returns
      a response to the correct channel
- [ ] Semantic memory: exchanges are stored and retrieved correctly;
      a query for a topic discussed previously returns relevant results
- [ ] Episodic memory: recent episodes load correctly at session start
- [ ] Intent store: an intent created via `chandra intent add` persists across
      `chandrad` restarts and appears in `chandra intent list`
- [ ] Scheduler: a time-based intent fires within 2x the tick interval of
      its due time and emits a proactive message to Discord
- [ ] Identity: agent and user profiles load from DB; relationship state
      updates after sessions
- [ ] Context budget: token counts are accurate; no LLM call exceeds the
      configured model's context limit
- [ ] Context budget adversarial test: with 1000+ memories, 50+ active intents,
      and large identity context all competing, CBM still produces valid output
      under the token limit (no overflow, no panic)
- [ ] Tool execution: at least 3 built-in skills (web search, HomeAssistant,
      MQTT publish) register, execute, and record telemetry correctly
- [ ] Tier 4 confirmation: a flagged tool call creates a confirmations row,
      does not execute, and executes correctly after approval
- [ ] MQTT events: a message published to a subscribed topic is received by
      the event bus and logged to episodic memory
- [ ] `chandra` CLI: start, stop, status, memory search, intent list/add/complete,
      tool list, log commands all work correctly
- [ ] Action log: every tool call, outbound message, and scheduled action is recorded
- [ ] Action rollups: hourly rollups generate correctly, `chandra log --today` shows summary
- [ ] Config safety: bad config change triggers automatic rollback to last known good
- [ ] Safe mode: `chandrad --safe` starts with minimal config, CLI works for recovery

### Non-Functional

- [ ] Cold start time (chandrad ready to accept messages): under 3 seconds
- [ ] Agent loop round-trip (message in → response out, no tools): under 5s
      excluding LLM latency
- [ ] Memory query (semantic search over 10k entries): under 100ms
- [ ] No goroutine leaks: `runtime.NumGoroutine()` stable under sustained load
- [ ] Graceful shutdown: SIGTERM causes clean shutdown within 5 seconds,
      no in-flight requests lost
- [ ] All packages have at least basic unit tests; memory layer has table-driven
      tests covering insert, query, and edge cases

### Code Quality

- [ ] `go vet ./...` passes with no warnings
- [ ] `go build ./...` succeeds with `CGO_ENABLED=1`
- [ ] No panics in normal operation paths — all errors returned, not panicked
- [ ] All exported types and functions have doc comments
- [ ] README.md covers: prerequisites, build, config, run, basic usage
