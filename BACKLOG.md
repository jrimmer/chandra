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

---

## Memory & Search

| # | Item | Priority | Source |
|---|------|----------|--------|
| M3 | Memory maintenance CLI — `chandra memory prune --older-than 90d`, `chandra memory stats` | P3 | PHASE2-DESIGN.md |
| M4 | Conversation summarisation — compress old episodic entries into semantic summaries to extend effective context window | P3 | Design |

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
| SK5 | Self-healing / error recovery — when a tool call fails, Chandra should suggest alternatives and retry intelligently (not just report failure) | P2 | Chandra's own suggestion |
| SK6 | Skill marketplace / sharing — publish and install skills across Chandra instances | P4 | Chandra's own suggestion |

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
| T1 | 2.4.x skills chaos — 2.4.4, 2.4.6–2.4.11 not yet run | P1 | TESTING.md |
| T2 | 2.5.x scheduler chaos — 2.5.3, 2.5.6–2.5.12 not yet run | P1 | TESTING.md |
| T3 | CI pipeline — no `.github/workflows/`; all tests run manually | P2 | TESTING.md |

---

*Last updated: 2026-03-06. Items added from: in-session Phase 1–3 work, context-loss bug analysis, Chandra's own backlog suggestions.*
