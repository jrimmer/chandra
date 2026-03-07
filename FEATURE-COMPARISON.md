# Chandra vs OpenClaw — Feature & Architecture Comparison

**Date:** 2026-03-07  
**Chandra:** `c0bad75` (deployed on chandra-test)  
**OpenClaw:** 2026.3.2 (`85377a2`)  
**Purpose:** Objective gap analysis for Chandra v1.0 readiness, with actionable recommendations.

---

## Scoring key

| Score | Meaning |
|-------|---------|
| ✅ | Equivalent or better than OpenClaw |
| 🟡 | Partial — works but with known gaps |
| ❌ | Missing entirely |
| 🔵 | Chandra-only (no OpenClaw equivalent) |
| N/A | Not applicable to Chandra's design goals |

---

## 1. Core Architecture

| Feature | OpenClaw | Chandra | Score | Notes |
|---------|---------|---------|-------|-------|
| Runtime | Node.js, WebSocket gateway | Go daemon, Unix socket API | ✅ | Chandra: lower memory, faster startup, static types |
| Config format | JSON (`openclaw.json`) | TOML (`config.toml`) | ✅ | Both validated at startup |
| Hot vs cold config | Hot reload only (restart required for most changes) | Explicit hot/cold classification + confirmation window for cold changes | 🔵 | Chandra more sophisticated |
| Health check | `openclaw status` (basic) | `chandra doctor` (8 checks, unmet deps, channel verification) | 🔵 | Chandra more detailed |
| Auto-update | Daily cron via skill | `chandrad-update` + version archive, rollback, changelog post | ✅ | Both adequate |
| API surface | WebSocket (JSON-RPC-like) | Unix socket (JSON-RPC 2.0) | ✅ | Both work |
| Multi-profile / isolation | `--profile <name>` for isolated state | Single config only | ❌ | Chandra has no dev/prod isolation |

---

## 2. Memory

| Feature | OpenClaw | Chandra | Score | Notes |
|---------|---------|---------|-------|-------|
| Short-term (episodic) | File-based (`memory/YYYY-MM-DD.md`) | SQLite with auto-write per turn | 🔵 | Chandra's is automatic and structured |
| Long-term (semantic) | File-based (`MEMORY.md`) + `memory_search` tool | Hybrid BM25 + vector (FTS5 + nomic-embed-text) | 🔵 | Chandra's retrieval is significantly more sophisticated |
| Episodic scope | Per-session (compacted into summaries) | Channel-scoped (`RecentInChannel`), unified across sessions/user_ids | ✅ | Fixed today; now includes proactive turns |
| Memory search | `memory_search` tool (semantic over .md files) | `note_context`, `forget_context`, `list_context`, inline semantic retrieval | ✅ | Different approach, both work |
| Memory maintenance | Manual (Sal curates MEMORY.md) | Not yet automated (`chandra memory prune` — M3 backlog) | 🟡 | Gap: no pruning of old episodes |
| User attribution in episodes | N/A (single user) | `role: user` only — no `user_id` stored per episode | 🟡 | Gap: multi-user channels lose attribution |
| Cross-restart persistence | Via files (survives restarts) | SQLite (survives restarts) | ✅ | Both work |
| Proactive turn visibility | N/A | Fixed today — `user_id="system"` sessions now visible to regular queries | ✅ | Was broken, now fixed |

---

## 3. Channels

| Feature | OpenClaw | Chandra | Score | Notes |
|---------|---------|---------|-------|-------|
| Discord | ✅ Full (bot + reactions + editing) | ✅ Full (bot + reactions + edit-in-place) | ✅ | |
| Telegram | ✅ (bot, send/delete/react/edit) | ❌ | ❌ | PD5 backlog, P3 |
| Signal | ✅ (via signal-cli) | ❌ | ❌ | Not planned |
| WhatsApp | ✅ (via wacli) | ❌ | ❌ | Not planned |
| iMessage | ✅ (macOS only) | ❌ | N/A | Wrong platform |
| Slack / IRC | ✅ | ❌ | ❌ | Not planned |
| Multi-channel simultaneous | ✅ | ❌ (single channel config) | ❌ | Chandra supports one channel at a time |
| Reply threading (Discord) | 🟡 (no referenced message injection) | ✅ Fixed today — `ReferencedContent` injected into LLM prompt | 🔵 | Chandra better than OpenClaw here |
| Require-mention gate | ✅ | 🟡 Currently `false` (coherence testing) — not flipped to `true` for v1 | 🟡 | **Action required for v1** |
| Bot filtering | ✅ | ✅ (`allow_bots` config) | ✅ | |
| Multi-user channel attribution | 🟡 (all messages attributed to one session) | ❌ (episodes lack `user_id`) | 🟡 | Low-effort fix when needed |

---

## 4. Skills

| Feature | OpenClaw | Chandra | Score | Notes |
|---------|---------|---------|-------|-------|
| Skill format | SKILL.md + frontmatter | SKILL.md + frontmatter | ✅ | Same design |
| Skill count (available) | 74 bundled + 32 custom | 8 deployed (unlimited custom) | 🟡 | OpenClaw has more bundled; Chandra builds locally |
| Skill marketplace | ClawHub (community sharing) | ❌ (SK6 backlog, P4) | ❌ | Not planned for v1 |
| Hot reload | ✅ | ✅ (`skill.reload` handler) | ✅ | |
| Skill authoring (self-modification) | ✅ (coding-agent skill) | 🔵 (`write_skill` + `read_skill` tools — Chandra writes her own skills) | 🔵 | Chandra's self-modification is first-class |
| Skill cron (scheduled turns) | Via OpenClaw cron system | 🔵 (`cron:` frontmatter, `CronSyncer`, 30m heartbeat) | 🔵 | Chandra-native design |
| Skill dependency validation | 🟡 (bins/env checked at load) | ✅ (bins/env/tools validated, `chandra skill list` shows unmet deps) | ✅ | |
| Skill commands (`!` dispatch) | ❌ | 🔵 (`commands:` frontmatter, skill-delegated `!` commands) | 🔵 | |
| Orphaned cron pruning | ❌ | 🔵 (`pruneOrphanedCronsLocked` on skill reload) | 🔵 | |
| Skill sandboxing | 🟡 (execution security policy) | 🟡 (dangerous-pattern gate on exec; no WASM/container isolation) | 🟡 | SK4 backlog, P3 |

---

## 5. Built-in Tools

| Feature | OpenClaw | Chandra | Score | Notes |
|---------|---------|---------|-------|-------|
| Web search | ✅ (Brave + Gemini grounding) | ✅ (web skill) | ✅ | |
| Read URL | ✅ (`web_fetch`) | 🟡 (web skill, no readability extraction) | 🟡 | W1 backlog, P2 |
| Shell exec | ✅ (`exec` tool, PTY, approval workflow) | ✅ (`exec` tool, 120s timeout, dangerous-pattern gate) | ✅ | OpenClaw has approval UI; Chandra has pattern gate |
| File read/write | ✅ (via `read`, `write` tools) | ✅ (`read_file`, `write_file`) | ✅ | |
| Browser automation | ✅ (Playwright, full browser) | ❌ | ❌ | W2 backlog, P4 |
| Schedule reminders | ✅ (cron system) | ✅ (`schedule_reminder` tool, recurring support) | ✅ | |
| List intents | ✅ (cron list) | ✅ (`list_intents` tool with kind + category filter) | ✅ | |
| Operator config | 🟡 (gateway restart only) | 🔵 (`set_config` with hot/cold classification, bounds checking, confirmation window) | 🔵 | |
| Token/cost visibility | 🟡 (`model-usage` skill) | 🔵 (`get_usage_stats` tool, per-conversation and daily aggregates) | 🔵 | |
| Conversation visibility | 🟡 (sessions_list, sessions_history) | 🔵 (`list_conversations`, `conversations.history` API) | ✅ | |
| Memory note / forget | ✅ (`memory_search`, `memory_get`) | ✅ (`note_context`, `forget_context`, `list_context`) | ✅ | |
| Tool reliability telemetry | ❌ | 🔵 (success rate, p50/p95 latency per tool) | 🔵 | |
| Exec approval workflow | ✅ (gateway approval queue, emoji reactions) | ❌ (pattern gate only) | 🟡 | Chandra trusts patterns; OpenClaw requires human approval |

---

## 6. Scheduling & Intents

| Feature | OpenClaw | Chandra | Score | Notes |
|---------|---------|---------|-------|-------|
| One-shot reminders | ✅ | ✅ | ✅ | |
| Recurring jobs | ✅ (cron expression) | ✅ (`recurrence_interval_ms`) | ✅ | |
| Skill-driven cron | 🟡 (skills can create cron jobs) | 🔵 (`cron:` frontmatter, auto-registered on skill load) | 🔵 | |
| Missed-job recovery | ✅ | ❌ (S2 backlog, P2) | ❌ | |
| Proactive backoff | 🟡 | ✅ (skips if channel active in last 5 min) | ✅ | |
| Error-after-N-failures alert | ❌ | ❌ (2.5.11 documented gap, S3 backlog, P2) | ❌ | Both missing |
| Scheduler chaos hardening | 🟡 | 🟡 (goroutine isolation; no duplicate-fire guard) | 🟡 | S3 backlog, P2 |

---

## 7. Progressive Delivery

| Feature | OpenClaw | Chandra | Score | Notes |
|---------|---------|---------|-------|-------|
| Reaction status (ack) | ✅ | ✅ (9 emoji states, 700ms debounce, 1500ms done hold) | ✅ | |
| Typing indicator | 🟡 | ✅ (typed with heartbeat) | ✅ | |
| Edit-in-place (placeholder → final) | ❌ | 🔵 (`DeliveryEditTarget` event, placeholder sent immediately, edited with final response) | 🔵 | |
| Token streaming | 🟡 (Claude streams, not exposed to UI) | ❌ (PD4 backlog, P3) | ❌ | |
| Reply threading on response | ✅ | ✅ (`ReplyToID` on OutboundMessage) | ✅ | |

---

## 8. Sub-agents & Parallelism

| Feature | OpenClaw | Chandra | Score | Notes |
|---------|---------|---------|-------|-------|
| Sub-agent spawning | ✅ (`sessions_spawn`, ACP harness) | ❌ | ❌ | Not planned |
| Parallel conversation handling | ✅ (each session is independent) | ✅ (per-conversation goroutines, `convQueues`) | ✅ | Different mechanism, equivalent result |
| Isolated coding sessions | ✅ (ACP + Codex/Claude Code) | 🟡 (Chandra uses `exec` tool + dev skill) | 🟡 | OpenClaw has dedicated coding harness |
| Agent-to-agent messaging | ✅ (`sessions_send`) | ❌ | ❌ | Not planned |
| Thread-bound persistent sessions | ✅ (Discord thread spawning) | ❌ | ❌ | Not planned |

---

## 9. Security & Access Control

| Feature | OpenClaw | Chandra | Score | Notes |
|---------|---------|---------|-------|-------|
| Allowlist enforcement | ✅ | ✅ (DB-backed, `!join` self-onboarding) | ✅ | |
| Empty allowlist hard error | 🟡 | ✅ (`allowed_users = []` is hard error, not silent open) | 🔵 | |
| HTTPS enforcement | 🟡 | ✅ (`set_config` rejects HTTP URLs) | 🔵 | |
| SSRF guard | 🟡 | ✅ (IP range validation on custom provider URLs) | 🔵 | |
| Rate limiting (per-user) | 🟡 | ❌ (A2 backlog, P2) | ❌ | |
| Request approval queue | 🟡 | ❌ (A3 backlog, P2) | ❌ | |
| Secrets in config | 🟡 (secrets.toml, env injection) | 🟡 (config.toml 0600, no external secret store) | 🟡 | I2 (keychain) deferred |
| Sandbox container isolation | ✅ (sandboxed exec sessions) | 🟡 (pattern gate, no container) | 🟡 | SK4 backlog, P3 |
| Exec approval workflow | ✅ | ❌ (pattern gate only) | 🟡 | |

---

## 10. Observability & Operations

| Feature | OpenClaw | Chandra | Score | Notes |
|---------|---------|---------|-------|-------|
| Structured logging | ✅ (slog JSON) | ✅ (slog JSON) | ✅ | |
| Action log | 🟡 | 🔵 (per-tool action log with type/detail/error) | 🔵 | |
| MQTT telemetry | ❌ | 🔵 (cron status, health metrics published to broker) | 🔵 | |
| Channel supervisor | ✅ (auto-reconnect) | ✅ (`ChannelSupervisor`, 1s→30s backoff) | ✅ | |
| Graceful shutdown | ✅ | ✅ (SIGTERM 35s wait, DrainPostProcess) | ✅ | |
| DB integrity checks | 🟡 | ✅ (`PRAGMA integrity_check` in doctor) | ✅ | |
| Disk usage monitoring | 🟡 | ❌ (I5 backlog, P2 — VM currently 96% full) | ❌ | **Operational risk** |
| CI pipeline | ❌ | ❌ (no `.github/workflows/`; tests run manually) | ❌ | T3 backlog |

---

## 11. Testing

| Feature | OpenClaw | Chandra | Score | Notes |
|---------|---------|---------|-------|-------|
| Unit tests | 🟡 (minimal) | ✅ (39 packages, ~200 tests) | 🔵 | |
| Integration tests | ❌ | ✅ (SQLite, memory, operator, e2e with loopback channel) | 🔵 | |
| Stub provider / loopback channel | ❌ | 🔵 (`internal/provider/stub`, `internal/channels/loopback`) | 🔵 | Shipped today |
| Chaos tests | ❌ | ✅ (T1/T2 — 15/15 PASS) | 🔵 | |
| Eval / benchmark framework | ❌ | 🟡 (EV1/EV2 backlog, P2) | ❌ | Both lacking |

---

## 12. Platform-specific (OpenClaw-only)

These features exist in OpenClaw with no Chandra equivalent and are not planned:

| Feature | OpenClaw | Recommendation |
|---------|---------|---------------|
| Canvas (web UI) | ✅ | **Never** — Chandra's UI is TUI (`chandra console`, SK2) |
| Nodes / device pairing | ✅ (camera, screen, notifications from phones) | **Never** — out of scope |
| Browser automation | ✅ (Playwright, full browser control) | **Later** (W2, P4) if web tasks demand it |
| Dashboard (web control UI) | ✅ | **Never** — `chandra console` TUI is the equivalent |
| QR code pairing | ✅ | **Never** — Discord-native auth sufficient |
| Tailscale DNS helpers | ✅ | **Never** — infra-specific to OpenClaw deployment |

---

## Recommendations

### 🔴 Do Now (v1.0 blockers)

| # | Action | Effort | Why |
|---|--------|--------|-----|
| V1 | **Flip `require_mention = true`** | 1 min | Config still set to `false` from coherence testing. v1.0 must not respond to every channel message. |
| V2 | **Fix duplicate T3 entries in BACKLOG.md** | 5 min | Two identical T3 entries appended; clean up. |
| V3 | **Monitor disk usage on chandra-test VM** | 1 hr | 96% full. One large rebuild could kill the VM. Add simple disk alert to heartbeat skill. |

### 🟡 Do Next (high value, low-to-medium effort)

| # | Action | Effort | Why |
|---|--------|--------|-----|
| N1 | **Add `user_id` to episodes table** (migration 011) | 1 day | Multi-user channels currently lose speaker attribution. Low schema effort, enables proper group chat. |
| N2 | **CI pipeline** (`go test` + `go vet` on push) | 1 day | Zero CI means regressions only caught on manual run. `.github/workflows/ci.yml` is 30 lines. |
| N3 | **Missed-job recovery on startup** (S2) | 1 day | Daemon restart silently drops overdue intents. Heartbeat catches this partially but not reliably. |
| N4 | **Rate limiting** (A2) | 1 day | No throttle per user. Public-facing v1 should have basic invite-attempt throttling. |
| N5 | **Telegram adapter** (PD5) | 3 days | Second channel immediately unlocks mobile/non-Discord usage. Design exists. |
| N6 | **Readability extraction on `read_url`** (W1) | 1 day | Raw HTML fetch floods context. Go `go-readability` would fix this cleanly. |
| N7 | **Eval framework** (EV1) | 2 days | No way to measure regression across model swaps. Even 10 structured prompts with expected outputs would catch the conversational disconnect class of bugs. |

### ⚪ Do Later (P3 — valuable but not urgent)

| # | Action | Effort | Why |
|---|--------|--------|-----|
| L1 | `chandra console` TUI (SK2) | 1 week | Nice-to-have; `!` commands + CLI cover most needs |
| L2 | Config schema parity CI check (R3) | 1 day | Good hygiene; not blocking |
| L3 | Conversation summarisation (M4) | 1 week | Useful once context window pressure is felt |
| L4 | `defer` tool (S4) | 2 days | Compelling UX but not blocking |
| L5 | Skill sandboxing (SK4) | 1 week | Pattern gate is sufficient for current users |
| L6 | Multi-turn plan state (SK8) | 2 weeks | Architecturally complex; worth doing after EV1 |

### ❌ Never / Not Applicable

| Feature | Reason |
|---------|--------|
| Canvas / web dashboard | Chandra is TUI-first; adding a web UI creates maintenance overhead with no gain |
| Device pairing (nodes) | Out of scope for a technical agent; no mobile use case identified |
| Agent-to-agent messaging | Chandra is single-agent by design; sub-agent complexity belongs in OpenClaw |
| Email integration | P4 in BACKLOG; only do as agent-mail pattern (dedicated programmatic inbox), not Gmail |
| Skill marketplace (SK6) | P4; local-first is the right default until there's actual sharing demand |
| macOS-specific tools | Wrong platform (Linux VM) |
| Keychain integration (I2) | Config at 0600 is adequate; keychain adds complexity for marginal security gain |

---

## v1.0 Verdict

**Chandra is functionally v1.0 ready** with three caveats:

1. `require_mention = true` — must be set before declaring v1
2. Disk space — 96% full is operational risk that should be addressed in the next 48h
3. The conversational disconnect root causes are now fixed (today's work), but the fix needs at least one multi-turn conversation to verify in production

The core conversation loop — memory, context retrieval, reply threading, proactive turns, progressive delivery — is sound. Chandra is ahead of OpenClaw in several areas (edit-in-place, hybrid memory retrieval, self-modification, operator tooling, test coverage).

The meaningful gaps vs OpenClaw (multi-channel, sub-agents, browser automation) are either planned or explicitly out of scope, not blockers for a v1 that serves one user on Discord.
