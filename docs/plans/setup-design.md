# Chandra Setup & Configuration

> First-run experience, configuration structure, and environment setup.

**Version:** 4.0 — 2026-03-03

---

## Design Priorities

This document reflects the intended design, not current implementation status. Features are ordered by build priority:

**Ship first:**
1. `chandra doctor` — stack health check, the "verifiably running" story
2. Security defaults — deny-all allowlists, HTTPS enforcement, secret masking

**Ship second:**
3. `chandra provider test` / `chandra channel test` — verification primitives
4. `chandra init` wizard — wraps the verification primitives, ends with Hello World loop confirmation
5. Access control system — invite codes, request flow, Discord role trust
6. Daemon lifecycle management (`chandra daemon install`)
7. `chandra console` — TUI admin console (see `CONSOLE.md`)

**TUI library decision: `huh` (Charm ecosystem)**
The init wizard and channel add flows need masked input, select menus, confirmation prompts, and progress indicators. `huh` (charmbracelet/huh) provides all of these with a form-oriented API that fits the wizard pattern — lighter than building a full bubbletea app for init, and shares the same Charm ecosystem as the full console (bubbletea + lipgloss). `survey` is archived/unmaintained. Raw ANSI is a maintenance burden. `huh` is the pick.

**Deferred (separate effort):**
- Telegram adapter
- Slack adapter
- Migration tooling (`chandra migrate openclaw`)
- Keychain/secret-store integration

---

## Implementation Reality

*This section is for implementers. It documents the gap between this spec and the current codebase, based on code review. The spec describes the correct end state — this section describes what needs to be fixed or built to get there.*

### Current CLI surface (as of March 2026)

What actually exists in `cmd/chandra/commands.go`:
```
start, stop, status, health
memory, intent, tool, skill, plan, infra, log, confirm
```

What this spec adds (all new):
```
init, doctor, provider test, channel test, channel add,
channel status, daemon install/start/stop/logs,
config show/validate/schema, security audit,
invite create/list/revoke, access add/remove/list/open,
console
```

None of the setup-facing commands exist yet. The entire setup surface is greenfield.

### Code contradictions to fix alongside new CLI work

These are places where the current code silently contradicts spec promises. They are not "features to build" — they are bugs to fix in existing code. An implementer who reads only this spec will be surprised by them.

**1. Channels are not optional (contradicts §1 stage table)**
- **Spec says:** CLI always works, channel setup is skippable
- **Code does:** `internal/config/config.go` validation requires a Discord channel to be configured before the daemon starts
- **Fix:** Make channel config optional in `config.go` validation. Daemon should start fine with CLI-only mode.

**2. `CHANDRA_CONFIG` env var is ignored (contradicts §7 env var table)**
- **Spec says:** `CHANDRA_CONFIG` overrides the config file path
- **Code does:** `resolveConfigPath()` in `cmd/chandrad/main.go` ignores it entirely; only `${VAR}` interpolation inside TOML is supported
- **Fix:** Read `CHANDRA_CONFIG` in `resolveConfigPath()` before falling back to the default path.

**3. Bad permissions warn but don't block (contradicts §8 and §12)**
- **Spec says:** Wrong file permissions block daemon startup
- **Code does:** `cmd/chandrad/main.go` checks permissions but only logs a warning and continues
- **Fix:** Turn the warning into a hard exit. One line change, high security value.

**4. `chandra start` is a dead command**
- **Spec says:** `chandra start` starts the daemon
- **Code does:** `cmd/chandra/commands.go` wires `chandra start` to call `daemon.start`, but no server handler for that RPC is registered — the call silently fails
- **Fix:** Register the handler, or remove the command until it's properly implemented to avoid false confidence.

**5. `daemon.health` reports optimistically (contradicts §4 doctor design)**
- **Spec says:** `chandra doctor` provides trustworthy end-to-end verification
- **Code does:** The current health endpoint reports provider as `"ok"` unconditionally and treats Discord as `"connected"` if a channel object exists in memory — not if a message was actually sent and received
- **Fix:** Do not extend this health endpoint into `chandra doctor`. Build doctor from scratch using the `DoctorCheck` interface (§4). The existing health endpoint can remain for backward compatibility but should not be the source of truth for verification.

### MVP build sequence

The right first deliverable is not a full wizard — it's "prove I can talk to Chandra over a real channel." Everything else follows from that.

**Phase 1: Make verification mean something (ship first)**

Fix the five code contradictions above. Then:

1. `chandra provider test` — expose existing provider connect + list-models as a CLI command. Small. Reuses existing provider construction code.
2. `chandra channel test discord` — send a message to a configured channel, watch for a reply to that specific message ID (not any message), report pass/fail. Persist `last_verified_at`, `verified_user_id` to DB.
3. `chandra doctor` — built on the `DoctorCheck` interface (§4). Reads the persisted verification timestamp for channel checks rather than running a live loop every time. Runs other checks (config, permissions, DB, provider) in parallel with 10s timeouts.
4. Security defaults enforcement: deny-all allowlist check at startup, HTTPS validation for custom providers.

At the end of Phase 1: `chandra doctor` passes only if the system is genuinely working. That's the foundation everything else is built on.

**Phase 2: Init wizard and access control (ship second)**

5. `chandra init` — wrap the Phase 1 primitives in an interactive wizard using `huh`. Stages: provider → channels → identity → config write → daemon install → doctor pass → open console. Checkpoint/resume from `~/.config/chandra/.init-checkpoint.json`.
6. Hello World loop — the `chandra channel test` live loop becomes the final channel verification step in init. The first replying user is added to the allowlist automatically.
7. Access control system — invite codes, request flow, role-based trust. New DB tables required. This is Medium effort and should not block init (init can bootstrap with a simple allowlist from the Hello World reply).
8. `chandra daemon install` — systemd unit (Linux) and launchd plist (macOS) generation.

**Phase 3: Console and extended tooling (ship third)**

9. `chandra console` — full TUI (see `CONSOLE.md`). Connects to running daemon via Unix socket.
10. `chandra security audit` — the full check table from §12.
11. Extended channel management — `channel add`, `channel status`, `invite` and `access` subcommands as standalone commands outside of init.

### What "verifiably connected" means, concretely

The definition of success for Phase 1 is: `chandra doctor` exits 0, and the output shows a channel loop test with a real timestamp — not a synthetic pass.

```
  Discord     ✓ Bot online · loop verified 2026-03-03 01:42 by Kaihanga
```

This timestamp comes from a real message-send + reply-receive event that was written to the DB. Until this exists, "configured" and "working" are the same word, and they shouldn't be.

### Performance note (not a setup blocker)

Semantic memory search is currently 3.2–3.45 seconds at 10k vectors (per `docs/done-criteria.md`). This does not affect setup, but it will hurt perceived responsiveness post-setup. It's a separate workstream and should not block any of the above phases, but it should be tracked and addressed before public release.

---

## The Full Journey

The complete path from nothing to a running, verified Chandra instance:

```
curl installer → binary in PATH → chandra init → provider test →
channel setup → Hello World loop → daemonize → doctor pass → console
```

Every step is either automatic or prompted with enough context that the user knows what they're doing and why. Setup ends by handing the user directly into the console — not a wall of text saying "you're done."

---

## 0. Installation

### One-line installer (recommended)

```bash
curl -fsSL https://get.chandra.ai | bash
```

The installer script (`install.sh`) does exactly four things:
1. Detects OS and architecture
2. Downloads the correct binary from the release page
3. Places it in `~/.local/bin` (or `/usr/local/bin` if run as root)
4. Verifies it's in `$PATH`, offers to add it if not

Then prints:
```
Chandra installed. ✓

Run 'chandra init' to get started.
```

That's it. The installer script does not run init, configure anything, or make decisions the user hasn't made. It only gets the binary onto the machine.

### Alternative install methods

```bash
# Homebrew (macOS / Linux)
brew install chandra-ai/tap/chandra

# Go toolchain
go install github.com/chandra-ai/chandra/cmd/chandra@latest

# Debian/Ubuntu
apt install chandra

# Manual: download from https://github.com/chandra-ai/chandra/releases
chmod +x chandra && mv chandra ~/.local/bin/
```

### Why a shell script for install but Go for everything else

The installer script is appropriate for exactly one thing: it runs before the binary exists. Once the binary is installed, all subsequent setup happens inside `chandra init`. A shell script that orchestrated multiple `chandra` subcommands would be fragile — no shared state between calls, error handling scattered across subprocess exits, and every edge case requires reinventing a state machine in bash. `chandra init` is a proper state machine in Go: it knows where it is, can checkpoint and resume, and handles errors with full context.

### First-run detection

If `chandra` is invoked with no config present, it guides the user in rather than erroring:

```
$ chandra

No configuration found at ~/.config/chandra/config.toml

Run 'chandra init' to set up Chandra, or see 'chandra --help'.
```

If `chandra init` is run when config already exists:

```
$ chandra init

Configuration already exists at ~/.config/chandra/config.toml
Last modified: 2026-02-15

  [R] Reconfigure (keep existing values as defaults)
  [F] Fresh start (archive and replace existing config)
  [Q] Cancel
```

---

## 1. First Run: `chandra init`

Interactive wizard for initial setup. Each step tests the connection before proceeding — the wizard cannot complete in a broken state. It ends by opening `chandra console` — the first thing the user does after setup is talk to Chandra.

**Init stages (in order):**

| Stage | What happens | Can skip? |
|---|---|---|
| Provider | Select LLM, enter API key, test connection | No — required |
| Channels | Select channels, enter tokens, Hello World loop | Yes — CLI always works |
| Identity | Name and persona | Yes — defaults are fine |
| Write config | Config + permissions + DB init | No — automatic |
| Daemonize | Install as system service, start daemon | Yes — can start manually |
| Doctor pass | Full stack verification, all green | No — blocks if critical check fails |
| Hand-off | Opens `chandra console` | — |

```
$ chandra init

Welcome to Chandra! Let's get you set up.

┌─ LLM Provider ─────────────────────────────────────────────┐
│                                                            │
│  Which provider will you use?                              │
│                                                            │
│  > OpenAI (api.openai.com)                                 │
│    Anthropic (api.anthropic.com)                           │
│    OpenRouter (openrouter.ai) — access to 100+ models      │
│    Ollama (local)                                          │
│    Custom OpenAI-compatible endpoint (HTTPS required)      │
│                                                            │
└────────────────────────────────────────────────────────────┘

[Select provider]

Enter your OpenAI API key: ••••••••••••••••••  ← input masked, no echo
Testing connection... ✓ Connected (gpt-4o available)

Default model [gpt-4o]: 

┌─ Channels ─────────────────────────────────────────────────┐
│                                                            │
│  How will you interact with Chandra?                       │
│  (Space to select, Enter to confirm)                       │
│                                                            │
│  [x] CLI (always enabled)                                  │
│  [ ] Discord                                               │
│                                                            │
│  (Telegram and Slack — coming soon)                        │
│                                                            │
└────────────────────────────────────────────────────────────┘

[If Discord selected]
Discord bot token: ••••••••••••••••••  ← input masked

Testing Discord connection... ✓ Bot online (ChandraBot#1234)

Which channel should I post the setup verification message to?
Channel name or ID: general

┌─ Access Policy ────────────────────────────────────────────┐
│                                                            │
│  Who can talk to Chandra on Discord?                       │
│                                                            │
│  > Invite codes  — you share codes, users redeem them      │
│    Request flow  — anyone can request, you approve         │
│    Role-based    — trust members of a Discord role         │
│    Allowlist     — paste user IDs manually                 │
│                                                            │
└────────────────────────────────────────────────────────────┘

[invite codes selected — more detail in Access Control section]

┌─ Identity (Optional) ──────────────────────────────────────┐
│                                                            │
│  Give your assistant a name? [Chandra]:                    │
│  Brief personality description? [blank for default]:       │
│                                                            │
└────────────────────────────────────────────────────────────┘

[Step 1/6] Writing config...          ✓
[Step 2/6] Setting permissions...     ✓ (config: 0600, dir: 0700)
[Step 3/6] Initializing database...   ✓
[Step 4/6] Running migrations...      ✓
[Step 5/6] Starting listener...       ✓
[Step 6/6] Sending verification...    ✓

Sent to #general:
  "👋 Hi! I'm Chandra. Reply to this message to complete setup."

Waiting for your reply... (2 min timeout) ████░░░░░░

  ✓ Reply received from Kaihanga (id: 128604272551133184)
    Full loop confirmed — inbound and outbound working.

  Add Kaihanga as an authorized user? [Y/n]: Y
  ✓ Kaihanga added to allowlist

┌─ Start on login? ──────────────────────────────────────────┐
│                                                            │
│  Install Chandra as a background service so it starts      │
│  automatically and runs persistently?                      │
│                                                            │
│  > Yes — install as system service (recommended)           │
│    No — I'll start chandrad manually                       │
│                                                            │
│  Detected: Linux (systemd)                                 │
│                                                            │
└────────────────────────────────────────────────────────────┘

Installing service... ✓
  Created ~/.config/systemd/user/chandra.service
  Enabled for current user (no root required)
  Starting now...  ✓ chandrad running (pid 12345)

Running final verification...

  Config         ✓
  Permissions    ✓
  Database       ✓
  Provider       ✓ OpenAI / gpt-4o
  Discord        ✓ ChandraBot#1234 · loop verified
  Daemon         ✓ running (pid 12345)

Everything looks good. Opening console...
```
```

**Design rules for the wizard:**
- All secret inputs (API keys, bot tokens) use no-echo terminal mode
- Provider connection is tested before writing config
- Channel setup ends with a Hello World verification loop — full bidirectional confirmation required
- The Hello World reply bootstraps the allowlist — no manual ID lookup needed
- `allowed_users = []` is a hard error when a channel is enabled — the Hello World step resolves this
- Custom provider URLs must use HTTPS (validated before connection test)
- Setup ends with a mini doctor pass — all checks must be green before the console opens
- Setup ends by opening `chandra console` — the user's first action after setup is talking to Chandra, not reading instructions
- If the Hello World times out (2 min), config is saved but flagged as unverified — user is directed to `chandra channel test discord` to retry
- If the user declines daemon install, setup still completes — but `chandra doctor` will note the daemon is not running until they start it

### 1.1 Resume and Error Recovery

`chandra init` checkpoints its progress. If it's interrupted (Ctrl+C, crash, network error), re-running it offers to resume:

```
$ chandra init

Previous setup session found (interrupted 3 minutes ago).
Progress: provider ✓  channels ✗  identity —  daemon —

  [R] Resume from where you left off
  [S] Start over
  [Q] Cancel
```

Checkpoint state is written to `~/.config/chandra/.init-checkpoint.json` and deleted on successful completion. The checkpoint stores completed steps and their results — it never stores secrets (those are re-prompted).

If a specific step fails, the user can retry that step without re-running the entire wizard:

```
  Discord setup failed: bot token invalid (401 Unauthorized)

  [T] Try again with a new token
  [S] Skip Discord for now (can add later with 'chandra channel add discord')
  [?] Help: how do I get a Discord bot token?
```

Every failure offers at minimum: retry, skip, and help. Setup can only be blocked by provider failure — a working LLM connection is the one non-skippable requirement. Channel failures are always skippable.

### 1.2 Contextual Help

At any prompt, `[?]` shows contextual help without leaving the wizard:

```
Enter your OpenAI API key: [?]

  ┌─ Help: OpenAI API Key ───────────────────────────────────┐
  │                                                          │
  │  1. Go to https://platform.openai.com/api-keys           │
  │  2. Click "Create new secret key"                        │
  │  3. Copy the key — it starts with sk-...                 │
  │  4. You won't be able to see it again after creation     │
  │                                                          │
  │  The key is stored in config.toml (plaintext).           │
  │  Prefer setting CHANDRA_API_KEY in your shell profile    │
  │  and leaving api_key blank in config.                    │
  │                                                          │
  │  [Enter] Back to prompt                                  │
  └──────────────────────────────────────────────────────────┘
```

Help content covers: where to get tokens, what permissions are needed, security recommendations, and links to external setup guides (Discord dev portal, etc.).

### 1.3 Non-Interactive Setup

For automation/scripting:

```bash
# Minimal setup with environment variables
export CHANDRA_PROVIDER=openai
export CHANDRA_API_KEY=sk-...
export CHANDRA_MODEL=gpt-4o
chandra init --non-interactive

# With config file
chandra init --config /path/to/config.toml

# Docker/container setup
chandra init --non-interactive --db /data/chandra.db
```

Non-interactive mode skips channel setup by default (use `DISCORD_BOT_TOKEN` etc. to include channels).

---

## 2. Hello World Verification Loop

The final step of `chandra init` (and of `chandra channel test`) is a live bidirectional loop test. This is the difference between "configured" and "verifiably working."

### What it proves

| Check | How |
|---|---|
| Bot token valid | Bot came online |
| Bot has send permission | Message posted to channel |
| Bot has read permission | Reply was received |
| Inbound routing works | Reply processed by Chandra |
| Allowlist seeded correctly | Replying user added as authorized |

A provider connection test proves the LLM is reachable. The Hello World loop proves the *whole system* works end to end.

### Flow

```
[Step 5/6] Starting listener...    ✓
[Step 6/6] Sending verification... ✓

Sent to #general:
  "👋 Hi! I'm Chandra. Reply to this message to complete setup."

Waiting for your reply... (2 min timeout) ████░░░░░░
```

The bot watches for a reply to that specific message (by message ID). When one arrives:

```
✓ Reply received from Kaihanga (id: 128604272551133184)
  Full loop confirmed — inbound and outbound working.

Add Kaihanga as an authorized user? [Y/n]: Y
✓ Kaihanga added to allowlist

Setup complete. Chandra is ready.
```

### Edge cases

**Timeout (no reply in 2 minutes):**
```
⚠ No reply received. Config saved, but channel loop is unverified.
  Run 'chandra channel test discord' when ready to complete verification.
```
Setup is not failed — it's saved but flagged. `chandra doctor` will report the channel as unverified until the loop is confirmed.

**Wrong user replies first:**
```
Reply received from unknown user (id: 987654321)
Is this you? [y/N]: N

Waiting for the correct reply... ████░░░░░░
```
The bot rejects the unknown reply and keeps waiting. If the user confirms "yes that's me," they're added to the allowlist.

**Bot lacks read permission:**
The listener times out. Error message directs user to check Discord bot permissions (Message Content Intent, Read Message History).

### Standalone re-verification

```bash
chandra channel test discord
# Runs the Hello World loop against an existing configured channel.
# Useful after permission changes, token rotation, or to confirm after a timeout during init.
```

---

## 3. Access Control

Chandra supports four access policies for channel DMs. The policy is set per-channel during `chandra init` and can be changed later.

### Policy: `invite` (recommended default)

Admin generates codes proactively; users redeem them. No approval loop.

```bash
# Create a single-use invite code, valid for 7 days
chandra invite create
→ Code: chandra-inv-a7f3b9c2d1e4
  Uses: 1 · Expires: 2026-03-10

# Create a multi-use code (e.g., for a family group)
chandra invite create --uses 5 --ttl 30d
→ Code: chandra-inv-f8c2a1e6b3d9
  Uses: 5 · Expires: 2026-04-02

# List active invite codes
chandra invite list

# Revoke a code
chandra invite revoke chandra-inv-a7f3b9c2d1e4
```

User redeems by DMing the bot:
```
User: !join chandra-inv-a7f3b9c2d1e4
Bot:  ✓ Access granted. Hi, I'm Chandra — how can I help?
```

On redemption: user ID is added to the allowlist, code use count is decremented. Single-use codes are deleted on redemption. Expired or exhausted codes are rejected with a clear message.

**Code format:** `chandra-inv-` followed by 12 cryptographically random hex characters (~281 trillion combinations). Word-number combos (e.g., `CHANDRA-MAPLE-7749`) look friendlier but are brute-forceable (~20M combinations). Cryptographic tokens are not. Rate limiting provides defense in depth: `!join` is throttled to 5 attempts per user per hour; excess attempts are silently dropped (no error message that would help an attacker confirm they're close).

**Why this over OpenClaw's pairing model:**
OpenClaw's pairing flow sends the user a code and requires the admin to approve it — the user has to relay the code out-of-band, the admin has to be watching. Invite codes flip the direction: admin generates and distributes, user redeems. No approval loop, no polling, works fully async.

### Policy: `request` (user-initiated, admin-notified)

Anyone can request access; admin gets a rich notification and approves/denies.

```
Unknown user "Alice Smith" is requesting access on Discord.
Platform ID: 987654321
First message: "Hey, Jason said I could ask you for help with..."

[Approve] [Deny] [Block]
```

The notification is delivered to the admin's primary channel (wherever Chandra is already talking to them). One tap to approve — Alice is immediately added to the allowlist and the bot replies to her.

**vs. OpenClaw pairing:** OpenClaw shows the admin a code and a platform ID with no name context. Chandra's request flow shows display name, platform, and a message preview. The admin also doesn't need to know or run a CLI command — it's a push notification with inline action.

**Rate limiting:** `request` policy is throttled to 3 access requests per user per 24 hours, and a maximum of 20 pending requests in the queue at any time. Beyond that, new requests are silently dropped. This prevents a flood of requests from becoming a DoS on the admin's attention. Blocked users receive no feedback (to avoid confirming the bot is reachable).

**Configuration:**
```toml
[channels.discord]
access_policy = "request"
# Approved users accumulate in the allowlist automatically
```

### Policy: `role` (Discord only)

Trust all members of one or more Discord roles. Access is managed through Discord's own UI — no Chandra-specific flows for adding/removing users.

```toml
[channels.discord]
access_policy = "role"
allowed_roles = ["1234567890"]   # role IDs
```

```bash
# During init or post-setup:
chandra channel add discord --policy role
# Prompts for role IDs, validates they exist in the bot's guild
```

Useful when you already manage access through Discord roles (e.g., `@family`, `@team`) and don't want to maintain a separate Chandra allowlist.

### Policy: `allowlist` (static)

Explicit list of user IDs. The Hello World reply bootstraps this during init. Additional IDs can be added manually:

```bash
chandra access add discord 987654321
chandra access remove discord 987654321
chandra access list discord
```

```toml
[channels.discord]
access_policy = "allowlist"
allowed_users = ["128604272551133184"]
```

### Policy: `open`

Accept messages from any user. **Not recommended** — suitable only for local/trusted networks where the channel itself is already access-controlled.

### Policy comparison

| Policy | Admin effort | User experience | Best for |
|---|---|---|---|
| `invite` | Generate + share code | DM code to bot, instant access | Small groups, async onboarding |
| `request` | Tap approve on notification | Ask, wait for reply | Organic access requests |
| `role` | Manage Discord roles | Just message the bot | Discord-native teams |
| `allowlist` | Paste user IDs | Just message the bot | Known IDs, tight control |
| `open` | None | Just message the bot | Trusted private channels |

### Time-windowed open access

For onboarding sessions where you want to let multiple people in quickly:

```bash
chandra access open --duration 1h
# Anyone can message during this window; their IDs are recorded.
# Window closes automatically. Admin reviews and confirms who to keep.
```

**Watchdog requirement:** The time window must be enforced by the daemon, not just a timer. On every inbound message during an open window, the daemon checks whether the window has expired and closes it if so — this protects against clock drift, daemon restarts mid-window, or timer failures. On daemon start, any open window older than its stated duration is immediately closed and logged. The admin is notified when the window closes with a summary of who messaged during it.

---

## 4. `chandra doctor`

Single command that validates the entire stack. This is the primary verification story — if `doctor` passes, Chandra is correctly configured and running.

```
$ chandra doctor

Chandra Doctor — Stack Verification
────────────────────────────────────

  Config         ✓ Valid TOML, all required fields present
  Permissions    ✓ config.toml: 0600 · config dir: 0700
  Database       ✓ Accessible, integrity ok, migrations current
  Provider       ✓ OpenAI reachable, gpt-4o available
  Discord        ✓ Bot online (ChandraBot#1234), can send/receive
  Scheduler      ✓ Running, next heartbeat in 3m
  Daemon         ✓ chandrad running (pid 12345, uptime 4h 12m)

All checks passed. Chandra is healthy.
```

Failure output is explicit about what's wrong and how to fix it:

```
  Provider       ✗ Connection refused — check CHANDRA_API_KEY
                   Test manually: chandra provider test --verbose
```

**Checks performed:**
- Config file exists, parses cleanly, required fields populated
- File permissions: config 0600, config dir 0700
- `allowed_users` not empty on any enabled channel (warns if bypassed)
- Custom provider URL uses HTTPS
- Database file accessible, PRAGMA integrity_check passes, migrations up to date
- Provider HTTP reachable, API key valid, default model listed
- Each enabled channel: adapter connected, Hello World loop verified (timestamp of last successful test)
- Daemon socket accessible (if daemon mode expected)
- Scheduler running, heartbeat interval reasonable

`chandra doctor` exits 0 on all-pass, non-zero on any failure. Suitable for use in health check scripts.

**Execution model:** checks run in parallel with a 10-second timeout per check. Slow provider responses (OpenAI under load, slow Discord gateway connect) don't block the entire command. Partial results are reported as they complete — a spinner per check, replaced by ✓ or ✗ as each finishes. If a check times out, it reports as a warning rather than a hard failure, with a suggestion to retry.

**Architecture note:** each check is a `DoctorCheck` interface:
```go
type DoctorCheck interface {
    Name() string
    Run(ctx context.Context) DoctorResult
}

type DoctorResult struct {
    Status  CheckStatus  // Pass, Warn, Fail
    Detail  string
    Fix     string       // Human-readable remediation hint
}
```

This makes checks composable, individually testable, and reusable — `chandra init`'s final verification step runs the same `DoctorCheck` implementations, so the two are guaranteed to agree on what "healthy" means.

---

## 5. `chandra status`

Lightweight runtime status (queries the running daemon via socket):

```
$ chandra status

Chandra — running (pid 12345, uptime 4h 12m)

Provider    openai / gpt-4o
Channels    discord ✓
Scheduler   running · next heartbeat 3m
Memory      1,247 episodes · 8,432 vectors
DB          42 MB · WAL enabled

Last activity: 12 minutes ago (Discord)
```

If the daemon is not running, exits with a clear message:

```
Chandra daemon is not running. Start with: chandrad
```

---

## 6. Configuration File

Location: `~/.config/chandra/config.toml` (or `$CHANDRA_CONFIG`)

**Security note:** API keys stored in config are plaintext. Prefer environment variables for secrets. Keychain integration is planned for a future release.

```toml
# Chandra Configuration
# Generated by 'chandra init' — edit freely

#───────────────────────────────────────────────────────────────
# Identity
#───────────────────────────────────────────────────────────────
[identity]
name = "Chandra"
description = "A helpful personal assistant"
# Optional: path to detailed persona file
persona_file = ""  # e.g., "~/.config/chandra/persona.md"

#───────────────────────────────────────────────────────────────
# Database
#───────────────────────────────────────────────────────────────
[database]
path = "~/.config/chandra/chandra.db"
wal_mode = true

#───────────────────────────────────────────────────────────────
# Provider (LLM)
#───────────────────────────────────────────────────────────────
[provider]
type = "openai"                    # openai | anthropic | openrouter | ollama | custom
base_url = ""                      # Custom endpoints must use HTTPS
api_key = ""                       # Prefer CHANDRA_API_KEY env var over storing here
default_model = "gpt-4o"
embedding_model = "text-embedding-3-small"

# Model aliases (optional convenience)
[provider.aliases]
fast = "gpt-4o-mini"
smart = "gpt-4o"
reasoning = "o1-preview"

# Fallback chain (optional)
[provider.fallback]
enabled = false
chain = ["gpt-4o", "gpt-4o-mini"]

#───────────────────────────────────────────────────────────────
# Channels
#───────────────────────────────────────────────────────────────

# CLI is always available, no config needed

[channels.discord]
enabled = false
bot_token = ""                     # Prefer DISCORD_BOT_TOKEN env var

# Access policy — controls who can message the bot
# Options: invite | request | role | allowlist | open
# Default: invite (recommended — admin generates codes, users redeem)
access_policy = "invite"

# SECURITY: at least one entry required when channel is enabled.
# An empty allowlist is a hard error — the bot will refuse to start.
# The Hello World loop during 'chandra init' bootstraps this automatically.
allowed_guilds = []                # Empty = accept from any guild the bot is in
allowed_users = []                 # Populated by Hello World reply or 'chandra access add'

# Role-based access (access_policy = "role" only)
allowed_roles = []                 # Discord role IDs — members of these roles can message the bot

# Telegram and Slack — adapters not yet implemented

#───────────────────────────────────────────────────────────────
# Skills
#───────────────────────────────────────────────────────────────
[skills]
directory = "~/.config/chandra/skills"
priority = 0.7
max_context_tokens = 2000
max_matches = 3

#───────────────────────────────────────────────────────────────
# Scheduler
#───────────────────────────────────────────────────────────────
[scheduler]
enabled = true
heartbeat_interval = "5m"

#───────────────────────────────────────────────────────────────
# Tools
#───────────────────────────────────────────────────────────────
[tools]
max_concurrent = 5
max_rounds = 10

# Exec tool — broad defaults, review before enabling
# SECURITY: allowed_shells + working_directory control blast radius.
# Tighten these if you don't need full shell access.
[tools.exec]
allowed_shells = ["bash", "sh"]
working_directory = "~"
timeout = "5m"

#───────────────────────────────────────────────────────────────
# Budget & Limits
#───────────────────────────────────────────────────────────────
[budget]
max_tokens_per_turn = 8000
daily_token_limit = 0              # 0 = unlimited (global across all users)
daily_cost_limit = 0.0             # 0 = unlimited (USD, global)

# Per-user rate limits — prevents a single authorized user from burning the budget
# Applies to all non-admin users on all channels
[budget.per_user]
max_turns_per_hour = 0             # 0 = unlimited
daily_token_limit = 0              # 0 = unlimited
daily_cost_limit = 0.0             # 0 = unlimited (USD)

# Note: hourly action log rollups with LLM summarization are expensive.
# Disable if cost is a concern.

#───────────────────────────────────────────────────────────────
# Logging
#───────────────────────────────────────────────────────────────
[logging]
level = "info"                     # debug | info | warn | error
file = ""                          # Empty = stderr only
action_log_retention = "90d"

#───────────────────────────────────────────────────────────────
# Plans (Autonomy v2+)
#───────────────────────────────────────────────────────────────
[plans]
auto_rollback = false
checkpoint_timeout = "24h"
max_concurrent = 2
```

---

## 7. Environment Variables

All config values can be overridden via environment variables. **Prefer env vars for secrets** — they avoid plaintext in config files, though note they are visible in `/proc/*/environ` on Linux.

| Variable | Config Path | Description |
|----------|-------------|-------------|
| `CHANDRA_CONFIG` | — | Config file path (default: `~/.config/chandra/config.toml`) |
| `CHANDRA_DB` | `database.path` | Database path |
| `CHANDRA_PROVIDER` | `provider.type` | Provider type |
| `CHANDRA_API_KEY` | `provider.api_key` | Provider API key |
| `CHANDRA_MODEL` | `provider.default_model` | Default model |
| `CHANDRA_LOG_LEVEL` | `logging.level` | Log level |
| `DISCORD_BOT_TOKEN` | `channels.discord.bot_token` | Discord bot token |

Environment variables take precedence over config file values.

---

## 8. Directory Structure

After setup:

```
~/.config/chandra/
├── config.toml          # Main configuration (0600)
├── chandra.db           # SQLite database (all state)
├── skills/              # User skills (SKILL.md files)
│   ├── github/
│   │   └── SKILL.md
│   └── docker/
│       └── SKILL.md
└── persona.md           # Optional: detailed persona/identity
```

Config directory is enforced at 0700. Config file at 0600. Daemon will refuse to start if permissions are wrong.

---

## 9. Post-Setup Commands

```bash
# Verify the full stack
chandra doctor

# Show runtime status
chandra status

# Start the daemon
chandrad

# Start daemon in foreground (for debugging)
chandrad --foreground

# CLI chat mode (no daemon needed)
chandra chat

# Inspect and validate configuration
chandra config show
chandra config validate
chandra config schema > schema.json

# Add a channel after initial setup
chandra channel add discord

# Test connections
chandra provider test
chandra channel test discord

# Show per-channel status
chandra channel status
```

---

## 10. Daemon Lifecycle

```bash
# Install as system service (generates systemd unit or launchd plist)
chandra daemon install

# Start/stop/restart via service manager
chandra daemon start
chandra daemon stop
chandra daemon restart

# Tail daemon logs
chandra daemon logs
chandra daemon logs --follow

# Uninstall service
chandra daemon uninstall
```

`chandra daemon install` detects the OS and generates the appropriate service file:
- Linux: `~/.config/systemd/user/chandra.service` (user-level systemd unit)
- macOS: `~/Library/LaunchAgents/ai.chandra.daemon.plist`

---

## 11. Channel Setup Details

### 11.1 Discord

1. Create bot at <https://discord.com/developers/applications>
2. Enable **Message Content Intent** in Bot settings
3. Generate bot token
4. Invite bot to server — `chandra channel add discord` generates the invite URL for you (see below)

```bash
chandra channel add discord
# Prompts for bot token (masked)
# Validates token and reads bot's client ID
# Generates OAuth2 invite URL with exactly the right permissions pre-selected:

  Bot invite URL (open in browser to add to your server):
  https://discord.com/oauth2/authorize?client_id=123456789&scope=bot&permissions=68608

  Opening browser... (or copy the URL above)

# After you've added the bot to your server, press Enter to continue
# Tests connection and Hello World loop before saving
```

This eliminates the most error-prone manual step in Discord setup — users no longer need to navigate the permissions calculator or guess which checkboxes to enable. The URL is computed from the bot's client ID (read from the token) with the exact permission integer for Chandra's required scopes.

Required Discord permissions (permission integer: `68608`):
- Read Messages / View Channels
- Send Messages
- Add Reactions
- Read Message History

**Access control:** `allowed_users` must have at least one entry. An empty list is rejected at startup. `allowed_guilds` can remain empty (accepts from any guild the bot is in) — the token itself gates guild access.

### 11.2 `chandra channel status`

Shows live connection state and key stats for all configured channels:

```
$ chandra channel status

Channel     Status    Detail                          Last message
─────────────────────────────────────────────────────────────────
discord     ● Online  ChandraBot#1234 · 3 users       12 min ago
cli         ● Always  —                               4 min ago
telegram    ○ Off     not configured                  —
slack       ○ Off     not configured                  —
```

Per-channel detail (e.g., `chandra channel status discord`):
```
Discord — ChandraBot#1234
  Status        Online
  Policy        invite
  Authorized    3 users (Kaihanga, Alice, Bob)
  Active codes  2 invite codes
  Loop test     ✓ Verified 2026-03-01 14:22
  Last inbound  12 minutes ago
  Last outbound 12 minutes ago
  Guilds        1 (rimmer-home)
```

### 11.3 Telegram *(not yet implemented)*

Planned:
1. Create bot via @BotFather
2. Get bot token

```bash
chandra channel add telegram  # future
```

### 11.4 Slack *(not yet implemented)*

Planned:
1. Create Slack app at <https://api.slack.com/apps>
2. Enable Socket Mode
3. Add bot scopes: `chat:write`, `app_mentions:read`, `im:history`, `im:read`, `im:write`
4. Install to workspace

```bash
chandra channel add slack  # future
```

---

## 12. Security Considerations

**API key storage:** Keys in `config.toml` are plaintext. Until keychain integration ships, prefer env vars (`CHANDRA_API_KEY`, `DISCORD_BOT_TOKEN`) set in your shell profile rather than writing them to the config file.

**Channel access defaults:** `allowed_users = []` on an enabled channel is treated as a misconfiguration, not an open policy. Chandra will refuse to start with an empty allowlist.

**Exec tool scope:** The default exec tool config allows arbitrary bash in `~`. If you don't need shell access, restrict `allowed_shells` or set a tighter `working_directory`. All exec calls still require confirmation by default.

**Custom providers:** `base_url` must use HTTPS. HTTP endpoints are rejected.

**MQTT broker:** Default bind is `localhost:1883`, unencrypted, no auth. Acceptable for local use. Do not expose externally without adding authentication.

**Audit command:**

```bash
chandra security audit
```

Runs the same `DoctorCheck` pattern as `chandra doctor` but security-focused. Checks performed:

| Check | Pass condition |
|---|---|
| Allowlist populated | All enabled channels have ≥1 authorized user |
| Access policy | No enabled channel is set to `open` (warns, not error) |
| API key storage | `api_key` field in config is empty (key is in env var) |
| Bot token storage | `bot_token` fields in config are empty (tokens in env vars) |
| Provider URL | Custom `base_url` uses HTTPS, not HTTP |
| Exec working dir | `tools.exec.working_directory` is not `/` or `/root` |
| Exec timeout | `tools.exec.timeout` is ≤ 5 minutes |
| Socket permissions | Unix socket is 0600 |
| Config permissions | `config.toml` is 0600, config dir is 0700 |
| Per-user budget | `budget.per_user` limits set (warns if all zeros with multiple users) |
| Open time window | No active open-access window currently running |

`chandra security audit` exits 0 if no errors (warnings don't count). Suitable for use in CI or scheduled security checks.

**OAuth2 token parsing note:** `chandra channel add discord` extracts the bot's client ID from the first segment of the bot token (base64-encoded). This is a widely-used technique but depends on Discord's current token format. If the parse fails, the command falls back to prompting for the client ID manually — setup continues, the invite URL is just generated from the user-supplied ID instead.

---

## 13. Troubleshooting

### Full Stack Check

```bash
chandra doctor
```

This should be the first step for any issue.

### Database Issues

```bash
chandra db check     # PRAGMA integrity_check
chandra db vacuum    # Reclaim space
chandra db reset --confirm   # Destructive — wipes all state
```

### Provider Issues

```bash
chandra provider test           # Connection + auth check
chandra provider test --verbose # Show request/response detail
chandra provider models         # List available models
chandra provider usage          # Token usage summary
```

### Channel Issues

```bash
chandra channel test discord    # Send test message, verify receipt
chandra channel status          # All channels and connection state
```

### Configuration Issues

```bash
chandra config validate         # Parse and validate config.toml
chandra config show             # Pretty-print resolved config (secrets masked)
```

---

## 14. Migration from OpenClaw *(deferred)*

Planned for a future release:

```bash
chandra migrate openclaw --source ~/.openclaw
```

Would import: memory files → semantic memory, config → Chandra config format, skills → skills directory.
