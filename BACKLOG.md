# Chandra Backlog

Consolidated, prioritized list of deferred work. Sources: Reddit OpenClaw ideas, PHASE2-DESIGN.md, RELIABILITY.md, TESTING.md, SETUP.md, PROGRESSIVE-DELIVERY.md, and in-session discoveries.

Items are grouped by theme, each with a priority and source reference.

**Priority key:** P1 = next sprint · P2 = near-term · P3 = backlog · P4 = future/aspirational

---

## ✅ Recently Completed

| Item | Commit | Notes |
|------|--------|-------|
| Hybrid BM25 + vector search (M1) | `5dec209` | FTS5 + RRF k=60; graceful degradation to vector-only |
| Episodic memory scoped by user_id (M2) | `9a81294` | migration 008; `""` = admin no-filter |
| `!join` self-onboarding (A1) | `7ddf4f4` | pre-gate handler; invite code redemption via Discord |
| Recurring scheduled jobs (S1) | `9337d24` | migration 009; `Reschedule()`; `interval` param on `schedule_reminder` |
| ChannelSupervisor + exponential backoff (R1) | `8481249` | 1s→30s cap; `chandra health` shows live state |
| Heartbeat skill + skill cron system (SK1+SK3) | `69ffd89` | `CronConfig` in frontmatter; `CronSyncer` interface; 30m heartbeat |
| Stale socket auto-cleanup (I1) | — | `net.Dial` probe in `api/server.go`; removes dead socket on startup |
| Progressive delivery Layer 0+1 (reactions + typing) | `8ee8064` | 9 emoji states; 700ms debounce; typing heartbeat |
| `write_skill` tool | `4feb77b` | self-modification loop; Chandra can author her own skills |
| `read_file` / `write_file` / `exec` tools (Phase 1) | `baf51c4` | local + SSH remote; dangerous-pattern gate; 120s timeout |
| `persona_file` feature | `87b3b66` | hot-reloaded; overrides DB identity |
| Embed LRU cache | `embedcache` pkg | 256 entries, 5min TTL, thread-safe |
| Non-blocking post-processing | `54200d4` | Steps 8–9 async; `PostProcessDone` callback; ~100ms latency gain |
| DrainPostProcess on shutdown (Bug 1) | `435e56d` | `ppWg` WaitGroup; 35s SIGTERM wait; no context loss on restart |
| Duplicate response fix (Bug 2) | `aeda224` | `handlerOnce` + `seenMsgIDs` FIFO dedup cache |
| `require_mention` gate fix | `0a1d9be` | was inverted; defaults correctly now |
| Directedness gate | `9932ebf` | `bot_mentioned` / `is_reply_to_bot` meta; silent drop for ambient messages |
| Proactive backoff | `195a711` | skips scheduled turns if channel active in last 5 min |
| `list_intents` tool | `8f26b1b` | replaces `list_reminders`; kind + category filter |
| `!join` self-onboarding (A1) | `7ddf4f4` | invite code flow without CLI |
| Auto-update system | `908ef94` | `chandrad-autoupdate.sh`; Discord changelog post on success/rollback |
| Version archive + rollback | `6faafd4` | `/usr/local/lib/chandra/versions/`; keep last 3 |
| Episodic memory written synchronously (Bug fix) | `8fe8404` | prevents context loss on rapid follow-up replies |
| System prompt protected from token-budget eviction (Bug fix) | `3cbead7` | `Recency: time.Now()` on identity candidate; was always dropped first |
| Episodic history reversed to chronological order (Bug fix) | `2e94da2` | DESC query reversed before assembly; LLM was seeing history backwards |
| Persona: self-knowledge / read config | `2e30cdd` | teaches Chandra to use `read_file` instead of hallucinating own settings |
| Phase 3 seed skills | `baf51c4` | `dev/`, `hetzner/`, `github/`, `proxmox/`, `chandra-auto-update/` SKILL.md |
| INTERNALS.md architecture reference | `d912fd4` | `~/chandra/INTERNALS.md` |
| Per-conversation dispatch model | `011138c` | `convQueues`; one goroutine per conversation; cross-conv parallelism |
| Message chunking at 1900 chars | `b8849b3` | newline-aware; fixes HTTP 400 on long responses |
| **PD3: Edit-in-place delivery** (Layer 2) | `a39112a` | `Send()` returns message ID; `Edit()` on Channel interface; placeholder sent immediately, edited with final response; error edits placeholder with warning |
| **SK5: Self-healing persona** | `a39112a` | persona.md: retry with alternatives on tool failure before escalating |
| Scheduled turns suppress error messages | `4707e8d` | `RunScheduled()` filters "ran out of steps" as QUIET; heartbeat SKILL.md: must produce text or QUIET after tool calls |
| Skill deps manifest | pre-existing | Already built: `requires.bins/env/tools` in frontmatter; `ValidateRequirements()` + `exec.LookPath`; `chandra skill list` shows unmet deps; seed skills declare deps |

---

## Memory & Search

| # | Item | Priority | Source |
|---|------|----------|--------|
| M3 | Memory maintenance CLI — `chandra memory prune --older-than 90d`, `chandra memory stats` | P3 | PHASE2-DESIGN.md |
| M4 | Conversation summarisation — compress old episodic entries into semantic summaries to extend effective context window | P3 | Design |
| M5 | **User attribution in episodes** — `ALTER TABLE episodes ADD COLUMN user_id TEXT`; write path passes `session.UserID`; context assembly annotates turns as `[Username] content`. Low-effort schema change required for coherent multi-user channel support. Currently all users appear as `role: "user"` with no name. | P2 | 2026-03-07 feature comparison |

---

## Provider & Cost

| # | Item | Priority | Source |
|---|------|----------|--------|
| C1 | Two-tier provider: scaffolding vs chat — cheap/local model (Ollama) for internal tool work; reserve stronger model for user-facing turns | P2 | Reddit #1 |
| C2 | Token budget enforcement per-turn — hard cap with graceful truncation, not silent overflow | P3 | DESIGN.md |
| C3 | Cost tracking — per-conversation and per-day API cost visibility; currently flying blind | P3 | New |

---

## Web & Browser

| # | Item | Priority | Source |
|---|------|----------|--------|
| W1 | Smart `read_url` skill — Go readability extraction (strip boilerplate, enforce token budget) | P2 | Reddit #3 |
| W2 | Full browser automation skill — screenshots + form interaction without context flooding | P4 | Reddit #3 |

---

## Access Control

| # | Item | Priority | Source |
|---|------|----------|--------|
| A2 | Brute-force rate limiting — per-user invite attempt throttle (5/hour), per-user request throttle | P2 | TESTING.md 2.3.6–2.3.9 |
| A3 | Request policy queue — `access_policy = "request"`; push to admin for approve/deny | P2 | PHASE2-DESIGN.md §Access |
| A4 | `chandra access open --duration 5m` — time-windowed open access | P3 | TESTING.md 2.3.10–2.3.12 |

---

## Scheduler & Intents

| # | Item | Priority | Source |
|---|------|----------|--------|
| S2 | Missed-job recovery — detect overdue intents on restart; re-fire, skip, or log | P2 | TESTING.md 2.5.7–2.5.8 |
| S3 | Scheduler chaos hardening — goroutine pool, duplicate-fire guard, error handling + admin alert after N failures | P2 | TESTING.md 2.5.6–2.5.12 |
| S4 | `defer` tool — `defer(action, duration, cancellation_hint)`; "I'll proceed unless you stop me" pattern | P3 | PHASE2-DESIGN.md §1.6 |

---

## Reliability & Observability

| # | Item | Priority | Source |
|---|------|----------|--------|
| R2 | Failure classification — 401 → permanent, 429 → backoff, 5xx → transient; structured error codes | P2 | RELIABILITY.md R6 |
| R3 | Config schema parity CI check — `scripts/check-schema-parity/main.go`; catches struct-vs-TOML drift | P3 | RELIABILITY.md R3 |
| R4 | Webhook health alert — `chandra doctor` pings configured webhooks on startup | P3 | RELIABILITY.md R4 |
| R5 | DB health check — detect disk-full / FS errors mid-run; current `PingContext` insufficient for SQLite | P3 | TESTING.md 1.5.3 |

---

## Progressive Delivery

| # | Item | Priority | Source |
|---|------|----------|--------|
| PD3 | **Layer 2: edit-in-place** — send placeholder immediately, edit with final response; requires `channel.Edit()`, `Send()` returning message ID, `OutboundMessage.EditID` | **P1** | PROGRESSIVE-DELIVERY.md |
| PD4 | Layer 3: token streaming — stream tokens into message edit; requires Discord streaming support | P3 | PROGRESSIVE-DELIVERY.md |
| PD5 | Telegram / Signal adapter — deferred from Phase 1; design exists | P3 | SETUP.md §Deferred |

---

## Skills & Console

| # | Item | Priority | Source |
|---|------|----------|--------|
| SK2 | `chandra console` TUI — Bubble Tea terminal UI; inspect memory, sessions, skills interactively | P3 | CONSOLE.md |
| SK4 | Skill sandboxing — directory traversal guard, prompt injection guard in SKILL.md content | P3 | TESTING.md 2.4.6–2.4.7 |
| SK8 | **Multi-turn plan state** — hold a sequential plan across turns with interruption and resumption; plan state persisted in DB; user mid-plan questions pause not abort; resumption on next relevant message. Compelling but architecturally complex — do simpler wins first. | P3 | Chandra suggestion |
| SK5 | Self-healing / error recovery — when a tool call fails, Chandra should suggest alternatives and retry intelligently (not just report failure) | P2 | Chandra's own suggestion |
| SK6 | Skill marketplace / sharing — publish and install skills across Chandra instances | P4 | Chandra's own suggestion |

---

## Workers & Parallel Agent Execution

**Design doc:** `WORKERS.md` (2026-03-07) — approved design, implementation pending.

Workers extend Chandra's goroutine-per-conversation model with on-demand isolated agents spawnable mid-turn. The existing executor already dispatches tool calls in parallel, so multiple `spawn_agent` calls in one turn launch workers concurrently with no additional orchestration.

| # | Item | Priority | Notes |
|---|------|----------|-------|
| W1 | `internal/agent/worker/pool.go` — `Pool`, `Worker`, `WorkerTask`, `WorkerResult` structs + goroutine lifecycle | P2 | Core; blocks W2–W7 |
| W2 | `spawn_agent` + `await_agents` tools registered in main.go | P2 | Depends on W1 |
| W3 | Migration 012: `worker_id` column on `token_usage` table; itemized + rollup in `get_usage_stats` | P2 | Depends on W1 |
| W4 | Inactivity watchdog — cancel worker if no LLM activity for 5min (not wall-clock) | P2 | Depends on W1 |
| W5 | Edit-in-place progress updates from worker pool (N/M done counter in placeholder) | P2 | Depends on W1+W2 |
| W6 | Persona guidance: when to parallelize autonomously (WORKERS.md §Implicit parallelism) | P2 | Depends on W2 |
| W7 | `get_usage_stats` itemized breakdown: parent + per-worker subtotals + grand total | P2 | Depends on W3 |

**Decisions (locked):**
- Max concurrency: **3** (configurable via `identity.max_workers`)
- Context: workers inherit **semantic memory (read-only)** + explicit task context param; no episodic inheritance, no episodic writes
- Tool allowlist: exec/read_file/write_file/web_search/get_current_time/read_skill; NO spawn_agent (depth=1), set_config, write_skill, note_context, schedule_reminder
- Timeout: **inactivity-based** (5min without LLM activity), not wall-clock — preserves long active workloads
- Token tracking: per-worker itemized + subtotal rollup in `get_usage_stats`


---

## Infra & Ops

| # | Item | Priority | Source |
|---|------|----------|--------|
| I2 | Keychain / secret-service integration — macOS Keychain or Linux D-Bus for `secrets.toml` | P4 | RELIABILITY.md R2 |
| I3 | Automated service install — `chandrad` as systemd/launchd unit via `chandra init` | P3 | SETUP.md |
| I4 | Migration tooling — OpenClaw → Chandra memory/config migration | P4 | SETUP.md §14 |
| I5 | Disk usage monitoring — Chandra proactively alerts when disk exceeds threshold (VM currently at 96%) | P2 | Operational experience |

---

## Evaluation & Benchmarks

| # | Item | Priority | Source |
|---|------|----------|--------|
| EV1 | Evaluation framework — structured task suite with pass/fail criteria; track regression across model/config changes | P2 | Chandra's own suggestion |
| EV2 | Multi-turn coherence test suite — automated test that sends multi-step conversations and verifies context retention | P2 | In-session bugs |

---

## Email

| # | Item | Priority | Source |
|---|------|----------|--------|
| E1 | Email integration via dedicated programmatic inbox (Agent Mail pattern) — NOT direct Gmail | P4 | Reddit #4 |

---

## Testing Gaps (open test cases)

| # | Item | Priority | Source |
|---|------|----------|--------|
| ~~T1~~ | ~~2.4.x skills chaos~~ — **DONE** 2026-03-07, all pass | done | TESTING.md |
| ~~T2~~ | ~~2.5.x scheduler chaos~~ — **DONE** 2026-03-07, 15/15 pass; 2.5.11 gap in S3 | done | TESTING.md |
| ~~T3~~ | ~~Loopback integration harness~~ — **DONE** `c0bad75` | done | 2026-03-07 |
| T4 | CI pipeline — no `.github/workflows/`; all tests run manually | P2 | TESTING.md |

---

*Last updated: 2026-03-07. Added: M5, T4, W1–W7 (worker/parallel agent execution), feature comparison summary. Closed: T1 T2 T3.*

---

## Feature Comparison & v1.0 Analysis

**Document:** `FEATURE-COMPARISON.md` (2026-03-07)
**Summary:** Gap analysis vs OpenClaw across 12 feature areas.

**Chandra ahead:** edit-in-place delivery, hybrid BM25+vector memory, self-modification via write_skill, operator tooling, test coverage, reply threading, tool reliability telemetry.
**OpenClaw ahead:** multi-channel support (8 channels vs 1), sub-agents and ACP harness, browser automation, bundled skill library (74 vs 8).

### v1.0 blockers

| # | Action | Effort |
|---|--------|--------|
| V1 | Flip `require_mention = true` (currently false from coherence testing) | 1 min |
| V2 | Disk usage alert — chandra-test VM at 96% full; add check to heartbeat | 1 hr |

### Next priorities

| # | Item | Backlog ref | Priority |
|---|------|-------------|----------|
| 1 | User attribution in episodes | M5 | P2 |
| 2 | CI pipeline | T4 | P2 |
| 3 | Missed-job recovery on startup | S2 | P2 |
| 4 | Rate limiting per user | A2 | P2 |
| 5 | Eval framework | EV1 | P2 |
| 6 | Readability extraction on read_url | W1 | P2 |
| 7 | Telegram adapter | PD5 | P3 |

### Explicitly out of scope

Canvas, device pairing, agent-to-agent messaging, direct email, skill marketplace, macOS tools, keychain integration.
