# Chandra Backlog

Consolidated, prioritized list of deferred work. Sources: Reddit OpenClaw ideas (2026-03-05),
PHASE2-DESIGN.md, RELIABILITY.md, TESTING.md, SETUP.md, PROGRESSIVE-DELIVERY.md.

Items are grouped by theme, each with a priority and source reference.

**Priority key:** P1 = next sprint · P2 = near-term · P3 = backlog · P4 = future/aspirational

---

## Memory & Search

| # | Item | Priority | Source |
|---|------|----------|--------|
| M1 | **Hybrid memory search (BM25 + vector + reranker)** — SQLite FTS5 for keyword layer, reciprocal rank fusion to merge with KNN results, optional cross-encoder reranker via Ollama | **P1** | Reddit #2 |
| M2 | Scope episodic recall by channel/session — `RecentAcrossSessions` currently bleeds adversarial context across channels (jailbreaks in #chandra-chaos visible in #chandra-test) | P2 | Testing (2.2.x note) |
| M3 | Memory maintenance CLI — `chandra memory prune --older-than 90d`, `chandra memory stats` | P3 | PHASE2-DESIGN.md |

---

## Provider & Cost

| # | Item | Priority | Source |
|---|------|----------|--------|
| C1 | **Two-tier provider: scaffolding vs chat** — route internal/tool work (skill generation, planning, summarisation) to a cheap/local model (Ollama); reserve claude-sonnet for user-facing conversations | P2 | Reddit #1 |
| C2 | Token budget enforcement per-turn — hard cap with graceful truncation, not silent overflow | P3 | DESIGN.md |

---

## Web & Browser

| # | Item | Priority | Source |
|---|------|----------|--------|
| W1 | Smart `read_url` skill — Go readability extraction (strip boilerplate, enforce token budget); replaces raw HTML dumps in context | P2 | Reddit #3 |
| W2 | Full browser automation skill — screenshots + form interaction without context flooding; wraps Agent Browser or Playwright with extraction filter | P4 | Reddit #3 |

---

## Access Control

| # | Item | Priority | Source |
|---|------|----------|--------|
| A1 | `!join` bot command — user sends `!join <code>` in Discord; bot redeems invite code conversationally without requiring CLI | P2 | TESTING.md 2.3.6 |
| A2 | Brute-force rate limiting — per-user invite attempt throttle (5 attempts/hour), per-user request throttle (3 requests/24h) | P2 | TESTING.md 2.3.6–2.3.9 |
| A3 | Request policy queue — `access_policy = "request"`; unauthorized user triggers push to admin with approve/deny; TESTING.md 2.3.8–2.3.9 | P2 | PHASE2-DESIGN.md §Access |
| A4 | `chandra access open --duration 5m` — time-windowed open access; closes automatically; survives restart | P3 | TESTING.md 2.3.10–2.3.12 |

---

## Scheduler & Intents

| # | Item | Priority | Source |
|---|------|----------|--------|
| S1 | Recurring jobs — `schedule_recurring` tool; `inStore.Complete()` not called unconditionally; cron-style or interval repeat | P2 | TESTING.md 2.5.3 |
| S2 | Missed-job recovery — on daemon restart, detect overdue intents; re-fire, skip, or log missed — consistent documented behaviour | P2 | TESTING.md 2.5.7–2.5.8 |
| S3 | Scheduler chaos hardening — goroutine pool (no explosion on 50 simultaneous jobs), duplicate-fire guard (interval < execution time), consistent error handling + admin alert after N failures | P2 | TESTING.md 2.5.6–2.5.12 |
| S4 | `defer` tool — `defer(action, duration, cancellation_hint)`; composes scheduler + confirmation; "I'll proceed unless you stop me" pattern | P3 | PHASE2-DESIGN.md §1.6 |

---

## Reliability & Observability

| # | Item | Priority | Source |
|---|------|----------|--------|
| R1 | `ChannelSupervisor` with exponential backoff — Discord reconnect state machine; `status: "reconnecting"` visible in health; alert webhook on state transitions | P2 | RELIABILITY.md R5; TESTING.md 1.5.5 |
| R2 | Failure classification — 401 → permanent, 429 → backoff, 5xx → transient; structured error codes (`CHANDRA_E001` etc.) | P2 | RELIABILITY.md R6 |
| R3 | Config schema parity CI check — `scripts/check-schema-parity/main.go`; catches struct-vs-TOML drift; deferred until schema stabilises post-Phase 2 | P3 | RELIABILITY.md R3 |
| R4 | Webhook health alert — `chandra doctor` channels check pings configured webhooks on startup | P3 | RELIABILITY.md R4 |
| R5 | DB health check — detect disk-full / FS errors mid-run; current `PingContext` insufficient for SQLite in-process | P3 | TESTING.md 1.5.3 |

---

## Progressive Delivery

| # | Item | Priority | Source |
|---|------|----------|--------|
| PD1 | Layer 2: edit-in-place delivery — update the bot's placeholder message as the LLM generates; avoids "thinking…" dead air | P2 | PROGRESSIVE-DELIVERY.md |
| PD2 | Layer 3: token streaming — stream tokens into the message edit; requires Discord API streaming support | P3 | PROGRESSIVE-DELIVERY.md |
| PD3 | Telegram / Signal adapter — deferred from Phase 1; design exists | P3 | SETUP.md §Deferred |

---

## Skills & Console

| # | Item | Priority | Source |
|---|------|----------|--------|
| SK1 | Heartbeat skill — proactive agent; checks pending tasks, sends useful updates without being asked; what separates agent from chatbot | P2 | PHASE2-DESIGN.md §2.2 |
| SK2 | `chandra console` TUI — Bubble Tea terminal UI; read/write memory, inspect sessions, manage skills interactively | P3 | CONSOLE.md |
| SK3 | Skill cron jobs — skills that register recurring jobs on load; cleanup on skill delete/disable | P3 | TESTING.md 2.4.4, 2.4.10 |
| SK4 | Skill sandboxing — directory traversal guard, prompt injection guard in SKILL.md content | P3 | TESTING.md 2.4.6–2.4.7 |

---

## Infra & Ops

| # | Item | Priority | Source |
|---|------|----------|--------|
| I1 | Stale socket cleanup — daemon should remove `/run/user/1000/chandra/chandra.sock` on startup if PID is dead, not just fail | P1 | Testing session note |
| I2 | Keychain / secret-service integration — macOS Keychain or Linux D-Bus secret-service for `secrets.toml`; deferred as cross-platform complexity is high | P4 | RELIABILITY.md R2 |
| I3 | Automated service install — `chandrad` as systemd/launchd unit; `chandra init` currently says "manual start"; deferred | P3 | SETUP.md |
| I4 | Migration tooling — OpenClaw → Chandra memory/config migration | P4 | SETUP.md §14 |

---

## Email

| # | Item | Priority | Source |
|---|------|----------|--------|
| E1 | Email integration via dedicated programmatic inbox (Agent Mail pattern) — NOT direct Gmail; forwarding inbox only; triggers local workflows | P4 | Reddit #4 |

---

## Testing Gaps (open test cases)

| # | Item | Priority | Source |
|---|------|----------|--------|
| T1 | 2.4.x skills chaos — 2.4.4, 2.4.6–2.4.11 not yet run | P1 | TESTING.md |
| T2 | 2.5.x scheduler chaos — 2.5.3, 2.5.6–2.5.12 not yet run | P1 | TESTING.md |
| T3 | CI pipeline — no `.github/workflows/`; all tests run manually; deferred until Phase 2 scope locked | P2 | TESTING.md |

---

*Last updated: 2026-03-05. Items added from: Reddit r/openclaw post (2026-03-05), PHASE2-DESIGN.md, RELIABILITY.md, TESTING.md, SETUP.md, PROGRESSIVE-DELIVERY.md.*
