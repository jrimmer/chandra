# Chandra — Reliability Phase

> Operational excellence for sophisticated IT users: sensible defaults, communicative
> error handling, and graceful failure recovery.

**Status:** Design phase
**Last updated:** 2026-03-03
**Informed by:** trina-oc/Slay deployment experience (2026-03-01); reviewed by Opus 4.6 (2026-03-03)

---

## Guiding Principle

Chandra targets sophisticated IT users who can handle CLIs and read logs. The bar is
not zero friction — it's *reasonable friction with excellent feedback*. A sysadmin
should be able to diagnose and fix any problem within 2 minutes given good error
messages, not 20 minutes of trial and error.

---

## R1 — Validate at Configuration Write Time

### Problem
Invalid config is accepted silently and only fails at runtime, often with an opaque error.
Examples:
- `serviceAccountFile` pointing to a non-existent path
- `webhookUrl` that isn't reachable
- Auth profile referencing a non-existent secrets key

### Design

```go
// internal/config/validate.go

type ValidationError struct {
    Field      string
    Value      string
    Message    string
    Suggestion string // Actionable remediation hint
    DocLink    string // Link to docs for this field
}

type Validator interface {
    Validate(cfg *Config) []ValidationError
}
```

Validators run on:
1. `chandra config set <key> <value>` — validate the affected section
2. `chandrad` startup — validate full config, warn on issues, fatal on critical ones
3. `chandra doctor` — explicit full validation with human-readable output

**Example output:**
```
✗ channels.googlechat.serviceAccountFile: file not found
  Path: /home/deploy/.openclaw/googlechat-service-account.json
  Fix:  Download a service account JSON key from GCP and place it at this path.
  Docs: https://docs.chandra.ai/channels/googlechat#service-account
```

### Implementation Notes
- File existence checks, URL format validation, enum membership
- Non-fatal warnings vs. fatal errors (missing optional file = warn; missing required = fatal)
- Validators are per-section, registered in a registry — easy to add new ones
- **URL reachability checks must be async with a 2–3s timeout** — do not block `chandra config set` on a synchronous HTTP probe. If the probe times out, report a warning ("could not verify reachability") not a hard failure, so airgapped or slow networks don't break config writes.
- **SSRF guard on URL fields:** any validator that probes a URL (webhook URLs, custom provider base URLs) must reject RFC-1918 addresses (10.x, 172.16–31.x, 192.168.x) and loopback before making the request. An attacker-controlled config value could otherwise trigger internal network probes. Use the same SSRF blocklist used by the fetch tool.

---

## R2 — Auth Propagation and Actionable Auth Errors

### Prerequisite: Secrets Storage Decision

**Resolve this before implementing R2.** The auth fallback chain is only as secure as the storage layer it reads from. Options in priority order:

1. **Environment variables** (preferred, documented first in SETUP.md) — no file on disk, process-scoped, visible in `/proc/*/environ` (acceptable tradeoff for single-user deployments)
2. **Dedicated secrets file** `~/.config/chandra/secrets.toml` at 0600, separate from `config.toml` — plaintext but isolated, never committed to git, excluded from backup exports
3. **Keychain/secret-service** (deferred) — macOS Keychain or Linux secret-service via D-Bus; the right long-term answer but complex across platforms

The auth resolver abstraction must hide which storage backend is in use. When keychain support ships later, no call sites change — only the resolver implementation.

**For this phase:** implement env vars + dedicated secrets file. Document that `config.toml` should never contain raw API keys; `secrets.toml` is the right place if env vars aren't used. `chandra auth add <provider>` writes to `secrets.toml`, not `config.toml`.

**`secrets.toml` creation sequence — implement exactly this way:**

```go
// internal/auth/secrets.go

func writeSecretsFile(path string, content []byte) error {
    // Create the file with 0600 from the start using O_CREATE|O_EXCL.
    // Never create at a permissive mode and chmod afterward —
    // there is a window between create and chmod where another process
    // can read the file. os.OpenFile with the right flags eliminates the window.
    f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
    if err != nil {
        if os.IsExist(err) {
            // File already exists — open for overwrite, verify permissions first
            return updateSecretsFile(path, content)
        }
        return fmt.Errorf("create secrets file: %w", err)
    }
    defer f.Close()
    _, err = f.Write(content)
    return err
}

func updateSecretsFile(path string, content []byte) error {
    // Verify existing file is 0600 before writing.
    // If permissions are wrong, refuse to write and surface an actionable error.
    info, err := os.Stat(path)
    if err != nil {
        return err
    }
    if info.Mode().Perm() != 0600 {
        return fmt.Errorf(
            "secrets file %s has permissions %o, expected 0600 — fix with: chmod 0600 %s",
            path, info.Mode().Perm(), path,
        )
    }
    // Write atomically: write to temp file, rename into place.
    // Rename is atomic on POSIX — no partial-write window.
    tmp := path + ".tmp"
    if err := os.WriteFile(tmp, content, 0600); err != nil {
        return err
    }
    return os.Rename(tmp, path)
}
```

**Rules:**
- Create with `O_CREATE|O_EXCL` at `0600` — never `0644` then `chmod`
- On update: verify permissions are still `0600` before writing, refuse with actionable error if not
- Write updates atomically via temp file + rename — no partial-write window
- The config dir (`~/.config/chandra/`) must be `0700` before this file is created; check and enforce at daemon startup (already specced in SETUP.md §12)

### Problem
In OpenClaw, agent auth (`auth-profiles.json`) is entirely separate from gateway/daemon
auth with no propagation. A fresh install has no agent auth and the error message gives
no remediation path — just "configure auth for this agent."

### Design

```go
// internal/auth/resolver.go

// AuthResolver looks up credentials with a defined fallback chain:
// 1. Agent-specific auth profile
// 2. Daemon-level auth config
// 3. Environment variables (ANTHROPIC_API_KEY, OPENAI_API_KEY, etc.)
// 4. Error with actionable message

type AuthResolver struct {
    agentProfiles map[string]AuthProfile
    daemonConfig  *DaemonAuthConfig
}

func (r *AuthResolver) Resolve(provider string) (Credential, error) {
    // ... fallback chain
    return Credential{}, &AuthError{
        Provider:   provider,
        Suggestion: fmt.Sprintf("Set ANTHROPIC_API_KEY or run: chandra auth add %s", provider),
        DocLink:    "https://docs.chandra.ai/auth",
    }
}
```

**Auth error format:**
```
✗ No credentials found for provider "anthropic"
  Tried: agent profile → daemon config → ANTHROPIC_API_KEY env var
  Fix:   chandra auth add anthropic
  Docs:  https://docs.chandra.ai/auth
```

### Defaults
- Daemon auth propagates to all agents unless agent has an explicit override
- `chandra auth add <provider>` stores to daemon config and is immediately available to all agents

---

## R3 — Config Schema/Type Parity Enforcement

### Problem
A config field can be added to Go structs but missed in validation logic, or added to
validation but not documented. These gaps only surface at runtime.

(In OpenClaw, this exact class of bug was caught by an automated code review on PR #31238 —
a Zod schema was missing a field that had been added to the TypeScript type.)

### Design

Chandra uses Go struct tags as the source of truth. A CI check compares:
- Fields declared in `Config` struct (via reflection)
- Fields with registered validators
- Fields present in the JSON schema (generated from struct tags)
- Fields documented in the config reference

```go
// scripts/check-schema-parity/main.go
// Runs in CI: go run ./scripts/check-schema-parity
// Fails if any config field lacks a validator registration or doc entry
```

This is a script, not a feature — low effort, high safety net.

### Implementation Note: Doc Format Convention

The "fields documented in the config reference" check requires a structured format to be machine-readable. Decision: config field documentation lives in Go struct tags (`description`, `example`, `doc` tags) rather than prose markdown. The parity script reads struct tags directly — no separate doc file to keep in sync. Human-readable docs are generated from these tags.

```go
type DiscordConfig struct {
    BotToken string `toml:"bot_token" description:"Discord bot token" required:"true" secret:"true"`
    AllowedUsers []string `toml:"allowed_users" description:"User IDs allowed to message the bot" doc:"access-control"`
}
```

This convention must be established before R3 is implemented — retrofitting tags onto an existing struct is easy but must happen in one pass.

---

## R4 — Operational Visibility (`chandra status`)

### Problem
Diagnosing a misbehaving instance requires digging through logs. There's no single
command that answers "is everything working?"

### Design

```
$ chandra status

Chandra v0.3.1 — chandrad running (pid 12345, uptime 3d 4h)

  Channels
  ✓ discord          connected    last message 2m ago
  ✓ googlechat       connected    last message 47m ago

  Auth
  ✓ anthropic        valid        expires never (API key)
  ✗ openai           not configured

  Memory
  ✓ sqlite           ok           1,204 episodes / 342 semantic entries
  ✓ sqlite-vec       ok           vector index healthy

  Scheduler
  ✓ 12 jobs active   next: "Daily backup" in 2h 14m

  Recent errors      none in last 24h
```

**Implementation:** `chandra status` queries `chandrad` via Unix socket. Each subsystem exposes a `HealthCheck() HealthResult` method. Each check has a timeout — if a subsystem doesn't respond within 2s, status reports it as `timeout` rather than blocking the display.

**`/health` endpoint security:** if `chandrad` exposes an HTTP health endpoint (for container orchestration, uptime monitors, etc.), it must be loopback-only by default (`127.0.0.1` bind) or require an auth token. An unauthenticated endpoint on `0.0.0.0` leaks operational state — uptime, connected channels, episode counts, error history. The Unix socket path is the preferred query mechanism for `chandra status`; HTTP is opt-in.

### Channel Connectivity Check on Startup
- Webhook channels: HTTP GET to configured endpoint on startup, warn if unreachable
- Logs actionable message: "Google Chat webhook unreachable at https://... — check Tailscale Funnel"
- Same SSRF guard applies: reject RFC-1918 and loopback targets before probing

---

## R5 — Graceful Failure Recovery

### Problem
Transient failures (network blip, API timeout) shouldn't crash or permanently break
a running instance. Permanent failures (bad config, expired credentials) should
surface clearly rather than silently degrading.

### Design

```go
// internal/channel/supervisor.go

type ChannelSupervisor struct {
    channel     Channel
    backoff     *ExponentialBackoff // 1s → 2s → 4s → ... → 5m max
    state       ChannelState        // connected | reconnecting | failed
    failedAt    time.Time
    failReason  string
}

// States:
// connected    → normal operation
// reconnecting → transient failure, attempting recovery with backoff
// failed       → permanent failure (bad credentials, config error), requires operator action
```

**Failure classification:**
- HTTP 401/403 → permanent (credential/permission issue) → alert + stop retrying
- HTTP 429 → transient (rate limited) → backoff
- Network timeout → transient → backoff
- Config error → permanent → alert on startup, don't start channel

**Backoff jitter:** when multiple channels fail simultaneously (e.g., network blip affecting all channels), naive exponential backoff causes synchronized reconnect storms. Add ±20% random jitter to each backoff interval so channels don't all retry at the same time. The `ExponentialBackoff` implementation should include jitter by default.

**Alerting:** `chandrad` can POST to a configured webhook on state transitions: `connected → reconnecting → failed`. The alert webhook URL is subject to the same SSRF guard as all URL fields (reject RFC-1918, loopback). An attacker-controlled config value for the alert webhook could otherwise cause the supervisor to exfiltrate failure telemetry to an external URL.

---

## R6 — Structured Logging with Error Codes

### Problem
Text-based logs are hard to monitor programmatically. Errors need codes so that
monitoring tools, alerts, and `chandra status` can parse and categorize them.

### Design

```go
// internal/log/errors.go

const (
    ErrAuthMissing      = "CHANDRA_E001"
    ErrAuthExpired      = "CHANDRA_E002"
    ErrChannelUnreachable = "CHANDRA_E010"
    ErrChannelAuthFailed = "CHANDRA_E011"
    ErrConfigInvalid    = "CHANDRA_E020"
    ErrConfigFileMissing = "CHANDRA_E021"
    ErrToolExecFailed   = "CHANDRA_E030"
    // ...
)

type StructuredError struct {
    Code       string
    Message    string
    Field      string   // if config-related
    Suggestion string
    DocLink    string
}
```

Logs emit both human-readable and JSON lines (controlled by `--log-format json`).

**Log scrubbing:** structured logs must not contain credential values. The R2 auth resolver logs which fallback steps were tried (e.g., "tried ANTHROPIC_API_KEY env var") but never the value. A log scrubber runs over all log fields before output, replacing anything matching known secret patterns (key names ending in `_KEY`, `_TOKEN`, `_SECRET`, `_PASSWORD`) with `[redacted]`. This is especially important if logs are shipped to an external aggregator.

---

## Implementation Sequencing

| Phase | Item                        | Effort | Value |
|-------|-----------------------------|--------|-------|
| R3    | Schema parity CI check      | Low    | High  |
| R2    | Auth propagation + errors   | Medium | High  |
| R1    | Config validation at write  | Medium | High  |
| R4    | `chandra status` command    | Medium | High  |
| R5    | Graceful failure recovery   | High   | High  |
| R6    | Structured log error codes  | Low    | Medium|

**Recommended order:** secrets storage decision → R3 → R6 → R2 → R1 → R4 → R5

Resolve the secrets storage format (env vars + `secrets.toml`) before writing any auth code — it's the foundation R2 builds on and is hard to change later without breaking stored credentials.

R3 and R6 are infrastructure that everything else builds on — cheap, high value, no dependencies.
R2 before R1: auth errors are the most common first-run failure mode.
R4 and R5 are ongoing operational concerns; implement once the core is stable.

The SSRF guard (RFC-1918 blocklist) should be implemented once and shared across R1 (URL validation), R4 (startup connectivity checks), and R5 (alert webhook) rather than duplicated in each.

---

## Notes

- The OpenClaw deployment experience (2026-03-01) directly informed this document
- Several of these issues have been filed upstream as OpenClaw issues/PRs (#31236, #31238)
  but Chandra should not depend on upstream fixes — implement these natively
- `chandra doctor` is the single most impactful user-facing feature in this phase;
  it transforms "why isn't this working" from a debugging session into a 10-second check
