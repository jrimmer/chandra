# Chandra Worker Architecture — Parallel Agent Execution

**Status:** Design (approved, implementation pending)
**Priority:** P2
**Last updated:** 2026-03-07

---

## Overview

Chandra can spawn isolated worker agents mid-turn to execute tasks concurrently.
Workers are first-class goroutines that run their own LLM reasoning loops, report
progress as they go, and return results to the parent conversation for synthesis.

This is not a bolt-on feature — it extends the concurrency model Chandra was
built on from the start.

---

## Foundation: Chandra's Goroutine Model

Chandra's current concurrency model is deliberately designed for this:

```
Inbound message
       │
       ▼
  Router goroutine
  (fans messages into per-conversation queues)
       │
       ├─► convQueue["conv_A"] ──► Worker goroutine A ──► agentLoop.Run()
       │
       ├─► convQueue["conv_B"] ──► Worker goroutine B ──► agentLoop.Run()
       │
       └─► convQueue["conv_C"] ──► Worker goroutine C ──► agentLoop.Run()
```

Each conversation already gets its own goroutine (`ensureWorker` in
`cmd/chandrad/main.go`). Messages within a conversation are serialized;
messages *across* conversations execute in parallel. This eliminates lock
contention between conversations without sacrificing sequential consistency
within one.

The executor (`internal/tools/executor.go`) goes further: within a single turn,
**all tool calls execute concurrently via goroutines**:

```go
// executor.Execute — already parallel
for i, call := range calls {
    go func(idx int, c pkg.ToolCall) {
        results[idx] = e.dispatchOne(ctx, c)
    }(i, call)
}
```

Workers build directly on this. When Chandra calls `spawn_agent` three times in
one turn, those three calls hit the executor concurrently, launching three worker
goroutines simultaneously. The LLM needs only a single `await_agents` call to
collect all three results. **No additional orchestration layer is required** —
the existing executor handles the dispatch.

---

## Architecture

### Components

```
Parent conversation goroutine
│
│  Tool call round N:
│  ┌─────────────────────────────────────────────────────────┐
│  │ spawn_agent("audit server-1") ──► Worker goroutine 1    │
│  │ spawn_agent("audit server-2") ──► Worker goroutine 2    │  (concurrent,
│  │ spawn_agent("audit server-3") ──► Worker goroutine 3    │  via executor)
│  └─────────────────────────────────────────────────────────┘
│
│  Tool call round N+1:
│  ┌─────────────────────────────────────────────────────────┐
│  │ await_agents(["w1","w2","w3"]) ──► collect results      │
│  └─────────────────────────────────────────────────────────┘
│
│  LLM synthesizes results → sends reply to user
```

### Worker internals

Each worker is a lightweight agent loop:

```
spawn_agent(task, context?)
     │
     ▼
WorkerPool.Spawn()
     │
     ├── allocates worker ID
     ├── creates isolated context window (semantic memory read-only, no episodic)
     ├── injects task prompt + optional parent context
     ├── starts goroutine: agentLoop.RunWorker(workerCtx, task, resultCh)
     └── returns worker ID immediately
```

The worker goroutine runs the same LLM reasoning loop as a conversation, but:

- **Isolated context**: clean LLM context seeded only with the task + injected
  context. No parent conversation history.
- **Semantic memory**: read-only access to the shared semantic store (domain
  knowledge, skills, notes). Workers know what Chandra knows.
- **No episodic writes**: workers do not write to the episodic store.
  Their reasoning trace is returned as part of the result, not persisted.
- **Restricted tools**: safe subset only (see below).
- **Bounded**: max rounds + inactivity timeout (see below).

### WorkerPool (`internal/agent/worker/`)

```go
type Pool struct {
    maxWorkers int           // concurrent cap (default 3)
    mu         sync.Mutex
    active     map[string]*Worker
}

// Spawn starts a worker and returns its ID immediately.
// Returns ErrPoolFull if maxWorkers active workers exist.
func (p *Pool) Spawn(ctx context.Context, task WorkerTask) (string, error)

// Await blocks until all specified workers complete or ctx is cancelled.
// Returns results in the same order as workerIDs.
func (p *Pool) Await(ctx context.Context, workerIDs []string) ([]WorkerResult, error)

// Progress returns live status for a worker (for edit-in-place updates).
func (p *Pool) Progress(workerID string) WorkerStatus
```

### Tools exposed to LLM

**`spawn_agent(task, context?, timeout_secs?)`**
```json
{
  "task": "Audit server-1 (10.1.0.10) for open ports and failed logins",
  "context": "We're doing a full Hetzner security audit. SSH key: deploy@...",
  "timeout_secs": 300
}
```
Returns `{"worker_id": "w_abc123"}` immediately. Non-blocking.

**`await_agents(worker_ids)`**
```json
{
  "worker_ids": ["w_abc123", "w_def456", "w_ghi789"]
}
```
Blocks until all workers complete (or timeout). Returns:
```json
{
  "results": [
    {
      "worker_id": "w_abc123",
      "task": "Audit server-1",
      "status": "done",
      "output": "Port 22 open (expected). No unusual logins in last 7 days...",
      "tokens_used": 1243,
      "duration_secs": 47
    },
    ...
  ]
}
```

---

## Decisions

### 1. Max concurrency: **3**

Default pool cap of 3 concurrent workers. Configurable via config.toml
(`identity.max_workers`). Prevents cost runaway on large-N tasks while
still enabling meaningful parallelism. A fourth `spawn_agent` call while
the pool is full returns an error with a clear message ("pool full, retry
or await a running worker").

### 2. Context inheritance: **semantic read-only + explicit injection**

Workers inherit **semantic memory** (read-only): domain knowledge, skill
content, notes Chandra has stored. This makes workers competent without
bloating their context with irrelevant conversation history.

Workers do **not** inherit:
- Episodic history (conversation-specific, mostly irrelevant to a focused task)
- Parent conversation's active context window

The parent can pass relevant context explicitly via the `context` parameter
of `spawn_agent`. This keeps context lean and intentional.

Workers do **not** write to episodic memory. Their reasoning trace is
returned in the result object only.

### 3. Worker tool allowlist: **safe subset only**

Workers receive a restricted registry:
- ✅ `exec` (with approval flow intact)
- ✅ `read_file`, `write_file`
- ✅ `web_search`, `read_url`
- ✅ `get_current_time`
- ✅ `read_skill` (workers can consult skill docs)
- ❌ `spawn_agent` (no recursive spawning — depth capped at 1)
- ❌ `await_agents` (workers don't manage other workers)
- ❌ `set_config`, `write_skill` (no config or self-modification from workers)
- ❌ `note_context`, `forget_context` (no memory mutation from workers)
- ❌ `schedule_reminder` (workers don't create intents)

### 4. Timeout: **inactivity-based, default 5 minutes**

Workers use an **inactivity timeout**, not a wall-clock timeout. The timer
resets on every LLM response or tool call completion. If 5 minutes pass
with no observable progress, the worker is cancelled.

This is intentional: long but active workloads (large codebase analysis,
multi-step server audit) are not killed just because they take time. Only
truly stalled workers — no tokens in, no tokens out — are cut. As agent
workloads grow longer and more complex, a wall-clock timeout would be
increasingly wrong.

Implementation: a `lastActivity time.Time` updated on every round;
a watchdog goroutine checks every 30s and cancels the worker context if
`time.Since(lastActivity) > inactivityTimeout`.

### 5. Token tracking: **per-worker itemized + rollup**

Token usage is tracked per worker and rolled up to the parent conversation.
`get_usage_stats` returns an itemized breakdown when workers were involved:

```
Session token usage:
  Parent conversation:   2,341 tokens   ($0.0023)
  Worker w_abc123:       1,243 tokens   ($0.0012)  audit server-1
  Worker w_def456:       1,189 tokens   ($0.0012)  audit server-2
  Worker w_ghi789:         987 tokens   ($0.0010)  audit server-3
  ─────────────────────────────────────────────────
  Total:                 5,760 tokens   ($0.0057)
```

All worker tokens are attributed to the parent conversation's `session_id`
in the `token_usage` table, with a `worker_id` column for itemization.

---

## UX

### Explicit parallelism (user asks)

```
User: "Audit all 5 Hetzner servers for open ports — run them in parallel"

Chandra: Spinning up 5 server audits…
          ⠋ Auditing all 5 servers (0/5 done)
          ⠙ Auditing all 5 servers (2/5 done)
          ⠹ Auditing all 5 servers (4/5 done)
          ✅ All 5 done.

          Here's what I found: [synthesized report]
```

### Implicit parallelism (Chandra decides)

```
User: "Check the health of my Hetzner servers"

Chandra: [Internally recognizes 5 independent targets]
          Checking all 5 servers in parallel…
          ⠋ Checking 5 servers (0/5 done)
          ...
          ✅ Done.

          All 5 servers healthy. [details per server]
```

Implicit parallelism is driven by persona guidance instructing Chandra to
recognize independent subtasks and prefer workers over sequential loops.
No special code path — the LLM calls `spawn_agent` × N naturally.

### Edit-in-place progress

Workers report progress via a shared status channel. The parent's
edit-in-place placeholder is updated as each worker completes:

```
Checking 5 servers… (0/5 done)   ← placeholder on first turn
Checking 5 servers… (3/5 done)   ← edited as workers finish
Checking 5 servers… (5/5 done)   ← final state before synthesis
```

---

## Implementation Plan

| Step | Description | Effort |
|------|-------------|--------|
| W1 | `internal/agent/worker/pool.go` — `Pool`, `Worker`, `WorkerTask`, `WorkerResult` | 1 day |
| W2 | `internal/tools/worker/spawn_tool.go` + `await_tool.go` | 0.5 day |
| W3 | Token tracking: `worker_id` column on `token_usage` table (migration 012) | 0.5 day |
| W4 | Inactivity watchdog goroutine in worker loop | 0.5 day |
| W5 | Edit-in-place progress updates from worker pool | 0.5 day |
| W6 | Persona guidance: when to parallelize (heartbeat + dev skill context) | 0.5 day |
| W7 | `get_usage_stats` itemized breakdown with per-worker subtotals | 0.5 day |

**Total estimated effort:** ~4 days

---

## What This Is Not

Workers are not threads, not processes, not containers. They are goroutines
running Chandra's own reasoning loop with restricted permissions and an
isolated context window. The spawning cost is microseconds. The runtime
cost is LLM API calls — which is where the real expense lies, and why the
pool cap and inactivity timeout exist.

This design deliberately avoids the complexity of OpenClaw's ACP harness
(separate processes, session routing, agent-to-agent messaging). Workers
are lightweight, in-process, and disposable. They exist for the duration
of a task and leave no persistent state.

---

*Design by Sal + Kaihanga, 2026-03-07.*
*Related: `BACKLOG.md` (worker items), `INTERNALS.md` (goroutine model), `cmd/chandrad/main.go` (convQueues/ensureWorker).*
