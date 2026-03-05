# INTERNALS.md — Chandra Architecture Reference

How the daemon works, why it's structured that way, and what the non-obvious decisions are.

---

## Message Processing Pipeline

Every inbound Discord message follows this path. Steps on the **critical path** block the user reply; steps marked **background** do not.

```
1.  Receive inbound message (Discord WebSocket → channel)
2.  !join gate          — self-onboarding bypass before access check
3.  Access gate         — allowed_users lookup; drop if unauthorized
4.  Session hydrate     — GetOrCreate session from DB
5.  Enqueue             — convQueues[conversationID] ← convMsg
                          (per-conversation goroutine; cross-conv parallelism)

--- worker goroutine picks up ---

6.  Assemble context    — system prompt + session history + memory recall
7.  LLM call            — provider.Complete()  [Claude, Kimi, OpenAI, etc.]
8.  Send reply          — discordDC.Send()
                          ← user sees response here

--- background goroutine (non-blocking) ---

9.  Episodic store      — append turn to session history
10. Semantic store      — embed + store in semantic memory (Ollama)
11. Relationship update — extract/update entity graph
```

Steps 9–11 were originally synchronous, adding ~100–150ms to every turn. Moving them to `go func()` with a `context.Background()` + 30s timeout eliminated that tail latency. The `PostProcessDone func()` callback in `LoopConfig` exists purely for tests to wait for the goroutine before asserting memory state.

### Concurrency model

Each conversation (hash of `channelID + userID`) has one worker goroutine fed by a buffered channel (`convQueues map[string]chan convMsg`). Goroutines are created lazily on first message and persist until the supervisor exits.

- **Intra-conversation**: serialized (FIFO queue). No concurrent turns for the same user.
- **Cross-conversation**: fully parallel. Alice and Bob's turns run simultaneously.

The queue is capped at 32 messages. Overflow is dropped with a warning — intentional back-pressure.

---

## Memory Subsystem

Three layers, all stored in SQLite:

### Episodic memory
Append-only turn log. `(session_id, role, content, timestamp)`. Used to reconstruct conversation history for context assembly. No expiry; sessions are soft-deleted after 30 days of inactivity.

### Semantic memory (vector store)
Stores condensed facts extracted from conversations. Each entry: `(id, user_id, content, embedding BLOB, source, timestamp, importance)`.

**Scoping**: `user_id` column (migration 008) ensures each user's semantic memories are isolated. Admin queries pass `userID=""` to skip the filter and see everything. This prevents cross-channel bleed — the original bug that caused `#chandra-chaos` facts to appear in `#chandra-test` responses.

**Hybrid retrieval** (BM25 + vector KNN with RRF):
```
Query text
   ├─→  FTS5 BM25 rank      → top 3×N candidates
   └─→  Ollama embeddings   → KNN cosine similarity → top 3×N candidates
                ↓
        Reciprocal Rank Fusion (k=60)
                ↓
        top N fused results
```

RRF score: `Σ 1/(k + rank_i)` over all lists a document appears in. k=60 (Cormack, Clarke & Buettcher, SIGIR 2009). Candidate pool is 3× the requested topN to give fusion enough to work with. If BM25 fails (missing FTS5 build tag, corrupt index), falls back to vector-only gracefully.

**Build requirement**: `sqlite_fts5` build tag required. The `Makefile` enforces this — `go build` without `-tags sqlite_fts5` will compile but FTS5 queries will fail at runtime.

**Benchmarks** (`tests/benchmark/`):
| Scenario | Result |
|---|---|
| `BenchmarkSemanticSearch_10k-2` (QEMU 2-vCPU, 10k entries × 1536 dims) | 36ms/op, 287 allocs/op |
| Live DB (768 dims, ~10 entries) | <1ms/op |

### Embedding cache
`internal/provider/embedcache.CachingEmbedder` wraps any `Embedder` interface:
- **Capacity**: 256 entries (LRU eviction)
- **TTL**: 5 minutes
- **Key**: raw text content (not hash — avoids one allocation)
- **Batch-aware**: partial cache hits — cached embeddings returned immediately, uncached texts batched to the upstream provider
- **Thread-safe**: `sync.Mutex`
- **Errors not cached**: a failed embed doesn't poison the cache

Saves ~50–200ms per cache hit (Ollama `nomic-embed-text` round-trip on the test VM). Warm cache during a typical conversation: context assembly re-embeds the query text once per turn; if the same phrase appeared recently it's served from cache.

---

## Channel Supervision

The `ChannelSupervisor` (`internal/channels/supervisor.go`) wraps any `Channel` implementation:

```
discordgo session
    └── discord.Discord         (adapter: Listen, Send, Reconnect, ConnectionState)
            └── ChannelSupervisor   (lifecycle: backoff, state, health)
                    └── daemon routing goroutine
```

On `Listen()` error (non-context-cancel), the supervisor enters a reconnect loop:
- Backoff: 1s → 2s → 4s → 8s → 16s → 30s (cap, exponential)
- `MaxAttempts = 0`: retry forever
- State transitions: `connected` → `reconnecting` → `connected` (or `failed` on exhaustion)
- `ConnectionState()` is polled by `daemon.health` — Discord status in `chandra health` reflects real state, not a hardcoded `"ok"`

The `discordgo` library has its own internal reconnect logic, but it's invisible to Chandra — no observable state, no backoff control. The supervisor layer provides both.

---

## Skill Cron System

Skills can declare a recurring scheduler intent in their SKILL.md frontmatter:

```yaml
cron:
  interval: 30m        # Go duration + "d"/"w" suffixes
  prompt: "..."        # injected as the scheduled turn's system context
  channel: default     # "default" = first Discord channel in config
```

On `skillReg.Load()`, `syncCronsLocked()` calls `CronSyncer.UpsertSkillCron()` for each skill with a cron block. The syncer creates a recurring `Intent` in the DB with `Condition = "skill_cron:<skillName>"`. This sentinel condition is how skill-owned intents are identified without a separate DB column.

**Idempotent**: if an active intent already exists for the skill, upsert is a no-op. Daemon restarts don't create duplicate intents.

**QUIET suppression**: scheduled turns whose response is exactly `"QUIET"` (after `strings.TrimSpace`) are not delivered to Discord. Skills use this to "check in" without sending noise when there's nothing worth saying.

**Skill category**: skills declare `category: <string>` in frontmatter (e.g. `proactive`, `monitoring`, `utility`). `list_intents kind=skill category=proactive` uses this to filter. Category is resolved at query time from the live registry — no DB column needed.

---

## Security Boundaries

**HTTPS enforcement**: all non-Ollama provider base URLs must use HTTPS. Custom provider URLs are validated at config load time.

**SSRF guard**: RFC-1918 addresses (`10.x`, `172.16–31.x`, `192.168.x`, link-local) are rejected for provider URLs. Loopback (`127.x`, `::1`) is permitted — Ollama runs locally.

**Anthropic base URL quirk**: the Anthropic SDK appends `/v1` internally. Config must be `https://api.anthropic.com` without `/v1`. The config validator enforces this and prints a fix hint on violation.

**Tool name validation**: `^[a-zA-Z0-9_-]{1,128}$` enforced at `Registry.Register()` time. Invalid names panic at startup — deliberate, catches misconfigurations early.

**Secrets file permissions**: `CheckSecretsPermissions()` is called at daemon startup and at `SafeWriter.WriteConfig()`. Mode > 0600 is a hard failure with `chmod 600 <path>` hint.

**`allowed_users = []`**: treated as a hard error at startup, never silently open. Must be explicitly set to `access_policy = "open"` to allow all users.

---

## Provider Abstraction

All LLM calls go through `internal/provider`. Two concrete implementations:
- `anthropic/`: Anthropic Messages API (native)
- `openai/`: OpenAI Chat Completions API — also handles **OpenRouter**, any OpenAI-compatible endpoint

OpenRouter requires zero additional implementation: set `type = "openai"`, `base_url = "https://openrouter.ai/api/v1"`, and any model ID from the OpenRouter catalogue. The daemon logs `openai-compatible provider ready` at startup for both.

**Provider health probe**: `daemon.health` TCP-dials the provider API host:port with a 3s timeout. Detects network outages without burning API tokens. Port defaults to 443/80 based on scheme if not specified.

**Per-conversation model dispatch**: the model used for a turn can be overridden at the conversation level. Default is `cfg.Provider.DefaultModel`.

---

## Database Migrations

All schema changes go through `store/migrations/`. Applied in order at startup via `migrate.go`. Current migrations:

| # | Name | Key change |
|---|---|---|
| 001 | initial | sessions, memory_entries, action_log |
| 002 | fix_confirmations | confirmations schema fix |
| 003 | execution_plans | approved_commands, plan tables |
| 004 | channel_verifications | Discord verification state |
| 005 | access_control | allowed_users, invite_codes |
| 006 | intent_delivery | channel_id, user_id on intents |
| 007 | memory_fts | FTS5 virtual table on memory_entries |
| 008 | semantic_user_scope | user_id column on memory_entries |
| 009 | intent_recurrence | recurrence_interval_ms on intents |

Migrations are append-only. Never edit an existing migration; add a new one.
