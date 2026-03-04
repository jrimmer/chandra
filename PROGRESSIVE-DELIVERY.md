# Progressive Delivery

> Design spec for real-time activity feedback during agent processing.

**Status:** Design  
**Related:** `PHASE2-DESIGN.md`, `CONSOLE.md`, `PLUGINS.md`

---

## Problem

Chandra is silent while working. From the user's perspective, the conversation freezes for several seconds (or longer for complex tasks), then a complete response appears. There's no indication of:

- Whether the message was received
- What Chandra is doing
- How long it'll take
- Whether something went wrong mid-task

This is qualitatively different from how humans communicate. A person would say "one sec, looking that up" or "hold on, I'm checking the calendar". Silence feels broken.

---

## What We Want

A layered delivery model where the user sees activity as it happens, not just the final result:

```
User: "What's the weather and do I have anything on tomorrow?"

[1ms]   👀  (reaction — message received, thinking)
[200ms] ⚙️  (reaction changes — tools running)
        Chandra is typing...
[1.2s]  [message edit] ⚙️ Checking weather...
[1.8s]  [message edit] ⚙️ Checking calendar...
[2.4s]  [reaction → ✅]
        "It's 72°F and clear today. Tomorrow you have a 10am standup and lunch with..."
```

For a simple question with no tools:
```
User: "What's 42 × 7?"

[1ms]   👀  (reaction)
[800ms] [reaction → ✅]
        "294"
```

No intermediate noise for simple questions. Progressive updates only when there's real work to show.

---

## Layers

### Layer 0: Reaction-Based Status (cheapest, highest value)

React to the inbound message with an emoji that changes as work progresses:

| State             | Emoji | Trigger                                          |
|-------------------|-------|--------------------------------------------------|
| Received          | 👀    | Message enters the processing pipeline           |
| Tool running      | ⚙️    | Any tool call begins execution                   |
| Done / success    | ✅    | Response sent to channel                         |
| Error             | ❌    | Agent loop error, provider failure, timeout      |
| Waiting on user   | ❓    | Confirmation gate triggered (awaiting approval)  |

Discord supports `PUT /channels/{id}/messages/{mid}/reactions/{emoji}/@me`. The reaction is added to the *user's message*, not Chandra's reply — this is intentional; it's status on the input, not the output.

Only one reaction is active at a time. Transition: add new → remove old.

**Why this works:** Reactions are immediate (one HTTP call), require no message creation, and don't flood the channel. The user sees `👀` within ~50ms of sending.

### Layer 1: Typing Indicator

`POST /channels/{id}/typing` shows "Chandra is typing..." in the Discord UI.

- Send immediately on message receipt
- Lasts ~10s; re-send every 8s while processing
- Stop when response is sent (implicit: typing indicator disappears when a message is posted)

This is the cheapest "alive" signal after reactions. **Implement with Layer 0.**

### Layer 2: Progress Messages (edit-in-place)

For tasks that take >2s or involve multiple tool calls, post a working message and edit it:

```
[post]  ⚙️ Working...
[edit]  ⚙️ Checking weather (1/2)...
[edit]  ⚙️ Checking calendar (2/2)...
[delete or leave as ghost]
[post]  Final response text here
```

The working message is a *separate message*, not the final reply — this keeps the conversation clean. Once the final response is posted, the working message can be deleted or left as a ghost (dimmed, low signal).

**Rate limiting:** Discord allows ~5 edits/5s per message. For rapid tool calls, batch updates rather than editing on every tool start. A 500ms debounce on edits is sufficient.

**Decision: delete or keep?**  
Delete by default. A ghost message with 3 intermediate steps between the user's question and Chandra's answer adds noise. Keep only if explicitly useful (e.g., a long-running task where the steps are part of the value).

### Layer 3: LLM Token Streaming

Stream completion tokens to Discord as they're generated, editing the response message as each token arrives.

This is the full experience — users see the response being composed in real-time, like Claude in a browser. Requires:

- Discord message posted immediately with placeholder
- Edit on every ~100ms token batch (batching to stay under rate limits)
- Graceful fallback if edits are throttled

**Status:** Defer to Phase 2. Implement Layers 0–2 first.

---

## Architecture

### Event Model

The agent pipeline emits `DeliveryEvent`s that the channel adapter consumes:

```go
type DeliveryEventKind string

const (
    DeliveryReceived    DeliveryEventKind = "received"    // Layer 0: 👀
    DeliveryToolStart   DeliveryEventKind = "tool_start"  // Layer 0: ⚙️ + Layer 2
    DeliveryToolEnd     DeliveryEventKind = "tool_end"    // Layer 2 update
    DeliveryResponding  DeliveryEventKind = "responding"  // Layer 0: ✅ pending
    DeliveryDone        DeliveryEventKind = "done"        // Layer 0: ✅
    DeliveryError       DeliveryEventKind = "error"       // Layer 0: ❌
    DeliveryAwaitInput  DeliveryEventKind = "await_input" // Layer 0: ❓
)

type DeliveryEvent struct {
    Kind      DeliveryEventKind
    MessageID string            // original user message ID (for reactions)
    Detail    string            // human-readable, e.g. "Checking weather"
    ToolName  string            // populated for tool_start/tool_end
}
```

The agent loop already has natural hook points:
- On `MessageEnvelope` received → `DeliveryReceived`
- Before `executor.Execute()` → `DeliveryToolStart`
- After `executor.Execute()` → `DeliveryToolEnd`
- Before final `channel.Send()` → `DeliveryResponding`
- After successful send → `DeliveryDone`
- On `agent/loop error` → `DeliveryError`
- On confirmation gate trigger → `DeliveryAwaitInput`

### Channel Adapter Interface

Each channel adapter implements `DeliveryUpdater`:

```go
type DeliveryUpdater interface {
    // OnDeliveryEvent is called by the agent pipeline on each status change.
    // Implementations must be non-blocking (fire-and-forget or buffered).
    OnDeliveryEvent(ctx context.Context, evt DeliveryEvent)
}
```

The Discord adapter implements this with:
- Reaction management (add/remove via Discord API)
- Typing heartbeat goroutine
- Working-message lifecycle (post, edit, delete)

Adapters that don't support reactions (future: Telegram, Signal) implement a degraded version — typing indicator only, or a status prefix on the reply.

### Discord Implementation Sketch

```go
type discordDeliveryTracker struct {
    session   *discordgo.Session
    channelID string
    messageID string       // original user message
    workMsgID string       // working message ID (Layer 2)
    lastEmoji string       // currently active reaction
    stopTyping chan struct{}
}

func (t *discordDeliveryTracker) OnDeliveryEvent(ctx context.Context, evt DeliveryEvent) {
    switch evt.Kind {
    case DeliveryReceived:
        t.setReaction("👀")
        go t.typingHeartbeat(ctx)

    case DeliveryToolStart:
        t.setReaction("⚙️")
        t.updateWorkingMessage(evt.Detail)

    case DeliveryDone:
        close(t.stopTyping)
        t.setReaction("✅")
        t.deleteWorkingMessage()

    case DeliveryError:
        close(t.stopTyping)
        t.setReaction("❌")
        t.deleteWorkingMessage()

    case DeliveryAwaitInput:
        t.setReaction("❓")
    }
}
```

---

## Interaction Design Notes

### When NOT to show a working message (Layer 2)

- Response arrives in <1.5s — skip the intermediate message entirely; just react + reply
- Single tool call with instant result — tool_start and tool_end are close enough that editing adds no value
- The working message would appear and disappear faster than the user can read it

Threshold: only post a working message if total processing time is projected to exceed ~1.5s, OR if ≥2 distinct tool calls are made.

### Confirmation gate (❓ state)

When Chandra hits a confirmation gate (Tier 2 tool requiring approval), the status becomes ❓ and Chandra posts: *"I need your approval before [action]. Reply approve or deny."*

This is the only case where the reaction isn't a terminal state — it stays ❓ until the user responds.

### Error state

On error, react ❌ and post a brief error message if it's actionable ("Provider unreachable — will retry") or silent if it's transient noise. Don't expose raw stack traces.

---

## OpenClaw Reaction Pattern (inspiration)

OpenClaw uses a simpler version of this for its own responses — a reaction changes based on what it's doing. This confirms the pattern is usable and non-intrusive in practice. Chandra's version adds the working-message layer for complex multi-step tasks.

---

## Implementation Order

1. **Layer 0 + Layer 1** (reactions + typing) — 1 session, high value, low risk  
   Emit `DeliveryReceived`, `DeliveryToolStart`, `DeliveryDone`, `DeliveryError` from agent loop.  
   Discord adapter reacts + types.

2. **Layer 2** (working message) — 1 session, medium complexity  
   Add debounced edit logic to Discord adapter.  
   Implement 1.5s threshold check.

3. **Layer 3** (token streaming) — Phase 2  
   Requires provider streaming API support + Discord rate limit management.

---

## Open Questions

- **Working message placement:** Same message thread as the reply, or always in the channel root? → Channel root, consistent with how Chandra normally replies.
- **Multi-channel:** If a user talks to Chandra in two channels simultaneously, each channel gets its own tracker. Stateless per `MessageEnvelope`.
- **Telegram / Signal:** Both support typing indicators. Reactions are platform-specific — Telegram supports them; Signal does not. Degrade gracefully.
