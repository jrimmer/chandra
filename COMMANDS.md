# COMMANDS.md — Chandra `!` Command System

*Version 1.0 — 2026-03-07*

## Overview

Chandra supports `!` prefix commands for direct control operations. Commands are
platform-agnostic — they work identically over Discord, Telegram, Signal, or any
future channel adapter.

Commands are **not** routed through the LLM. Instant commands execute a Go handler
and return a string response with zero inference cost. Skill-delegated commands
force-activate a skill and route to a normal LLM turn, but with the skill context
guaranteed present.

---

## Architecture

### Command Registry

`internal/commands/registry.go` implements a `Registry` that maps command names to
`CommandDef` entries. Registered at daemon startup; skill commands are added/removed
on `chandra skill reload`.

```
CommandRegistry
  ├── built-in handlers (compiled Go)
  └── skill-delegated handlers (registered on skill load)
```

### Handler Types

**Instant** — pure Go function, no LLM call:

```go
type HandlerFunc func(ctx context.Context, cmd Command, env *Env) Result
```

`Result` carries:
- `Content string` — response text
- `Rerun bool` + `RerunMsg string` — for `!retry`: signal the conv worker to replay
  the last user message through the LLM path

**Skill-delegated** — routes to LLM with skill force-activated. The owning skill's
SKILL.md content is injected into the system prompt regardless of trigger matching,
then the command text is passed as the user turn.

### Env — Dependency Injection

```go
type Env struct {
    DB         *sql.DB
    Sessions   agent.Manager
    Scheduler  scheduler.Scheduler   // for !quiet: advance heartbeat next_check
    Skills     *skills.Registry       // for !skills
    Config     *config.Config
    StartedAt  time.Time             // for !status uptime
    ChannelID  string                // current channel (for !quiet, !sessions)
}
```

No globals. The registry is constructed at startup with all dependencies wired in.

### Per-Conversation State

`!model`, `!verbose`, and `!reasoning` set flags scoped to the current conversation
lifetime. State is stored as JSON in the `sessions.meta` column:

```json
{
  "model_override": "anthropic/claude-haiku-4-5",
  "verbose": true,
  "reasoning": false
}
```

The conv worker reads session meta before each LLM call and applies overrides.
Flags reset when the session expires or `!reset` closes it — intentional, since
`!reset` means "start clean".

### Placement in the Conv Worker

```
inbound message
 ├── config confirmation interceptor   (existing)
 ├── !join interceptor                 (existing)
 ├── [NEW] command interceptor         ← strings.HasPrefix(lower, "!")
 │    └── registry.Dispatch(ctx, cmd, env)
 │         ├── instant  → send response, continue (skip LLM)
 │         └── skill-delegate → mutate msg, fall through to LLM path
 └── access gate → LLM turn
```

Commands run after the config confirmation check (so `!reset` can't accidentally
dismiss a pending config confirmation) but before the access gate (same as `!join`).
Actually — they run **after** the access gate. `!join` is pre-gate because it's
the mechanism to join. All other commands require the user to already be allowed.

### `!retry` — Special Case

`!retry` cannot be handled purely inside the command handler because it needs to
re-enter the LLM path. The conv worker keeps a `lastUserMsg string` variable
tracking the last non-command user message. When `Dispatch()` returns `Result{Rerun: true}`,
the conv worker replaces `cm.msg.Content` with `lastUserMsg` and falls through to
the normal LLM turn (skipping the command interceptor on the re-run).

### `!quiet` — Scheduler Integration

Advances the heartbeat intent's `next_check` by the requested duration:

```
!quiet       → 2h default
!quiet 30m   → 30 minutes
!quiet 4h    → 4 hours
!quiet 24h   → 24 hours
```

Queries `intents WHERE condition = 'skill_cron:heartbeat'` and sets
`next_check = now + duration`. No new tables or intent types required.

---

## Built-in Commands

| Command | Args | Type | Description |
|---|---|---|---|
| `!help` | `[command]` | Instant | List all commands, or detail on one |
| `!reset` | — | Instant | Close session; next message starts fresh |
| `!retry` | — | Instant→Rerun | Re-run the last user message through LLM |
| `!status` | — | Instant | Daemon version, uptime, active sessions, tokens today |
| `!context` | — | Instant | Show ongoing_context + last 5 user episodes |
| `!skills` | — | Instant | List loaded skills (name, category, triggers) |
| `!sessions` | `[--limit N]` | Instant | Recent conversations with timestamps |
| `!usage` | — | Instant | Token usage today and all-time |
| `!quiet` | `[duration]` | Instant | Snooze heartbeat (default 2h) |
| `!model` | `[name]` | Instant | Show or set conversation model override |
| `!verbose` | — | Instant | Toggle verbose mode (show tool call details) |
| `!reasoning` | — | Instant | Toggle extended thinking mode |

---

## Skill-Exposed Commands

Skills declare commands in SKILL.md frontmatter:

```yaml
commands:
  - name: weather
    description: Get current weather for a location
    usage: "!weather [city]"
  - name: btc
    description: Current Bitcoin price
    usage: "!btc"
```

On `chandra skill reload`, skill commands are registered with `skillDelegate`
handlers. They appear in `!help` under a "Skill commands" section.

**Conflict resolution:** if two skills declare the same command name, the first
loaded skill wins and a warning is logged. Built-in commands always take precedence
over skill commands.

**Invocation flow:**
1. `!weather london` hits the command interceptor
2. Registry finds `weather` → skill-delegate handler for `weather` skill
3. Conv worker force-activates the weather skill in the system prompt
4. Normal LLM turn with `msg.Content = "!weather london"`
5. LLM responds with weather (guided by skill's SKILL.md)

---

## Adding New Built-in Commands

1. Add a handler function to `internal/commands/builtin.go`
2. Register it in `NewRegistry()` in `internal/commands/registry.go`
3. No other changes required — `!help` picks it up automatically

## Adding Skill-Exposed Commands

Add a `commands:` block to `SKILL.md`. No code changes required.

---

## Security

- All commands (except `!join`) run after the access gate. Only `allowed_users` can invoke them.
- `!model` accepts any model string but the provider will reject unknown models at call time.
- `!reset` closes the current session only — it cannot affect other users' sessions.
- `!quiet` only affects the heartbeat intent, not user-created reminders.
