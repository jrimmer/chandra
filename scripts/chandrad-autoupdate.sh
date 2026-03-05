#!/usr/bin/env bash
# chandrad-autoupdate.sh — fully autonomous update wrapper
#
# Checks for new commits on origin/main, builds, tests, and deploys.
# Delegates to chandrad-update for the actual binary swap + health check.
# chandrad-update posts a Discord notification with the changelog.
#
# Respects ~/.config/chandra/update.pin — if the file exists and is non-empty,
# the update is skipped and the pin is logged.
#
# Environment:
#   CHANDRA_REPO         Path to the Chandra git repo (auto-detected if not set)
#   CHANDRA_DISCORD_BOT_TOKEN    Discord bot token for notifications
#   CHANDRA_DISCORD_CHANNEL_ID   Discord channel ID to post to
#   CHANDRA_UPDATE_LOG   Log file path (default: /tmp/chandrad-autoupdate.log)

set -euo pipefail

LOG_FILE="${CHANDRA_UPDATE_LOG:-/tmp/chandrad-autoupdate.log}"
PIN_FILE="${HOME}/.config/chandra/update.pin"

log() { echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] $*" | tee -a "$LOG_FILE"; }

echo "" >> "$LOG_FILE"
log "══════════════════════════════════════"
log "chandrad-autoupdate started"

# ── 1. Pin check ──────────────────────────────────────────────────────────────

if [[ -f "$PIN_FILE" ]] && [[ -s "$PIN_FILE" ]]; then
    pin=$(cat "$PIN_FILE" | tr -d '[:space:]')
    log "Pinned to: ${pin} — skipping update."
    exit 0
fi

# ── 2. Locate repo ────────────────────────────────────────────────────────────

REPO="${CHANDRA_REPO:-}"
if [[ -z "$REPO" ]]; then
    for candidate in /home/deploy/chandra /root/chandra ~/chandra; do
        if [[ -d "${candidate}/.git" ]]; then
            REPO="$candidate"
            break
        fi
    done
fi
if [[ -z "$REPO" ]]; then
    log "ERROR: cannot locate Chandra git repo. Set CHANDRA_REPO."
    exit 1
fi
log "Repo: $REPO"

# ── 3. Fetch and check for new commits ───────────────────────────────────────

cd "$REPO"
export PATH="$PATH:/usr/local/go/bin"

git fetch origin main --quiet 2>&1 | tee -a "$LOG_FILE"

new_count=$(git rev-list HEAD..origin/main --count)
if [[ "$new_count" -eq 0 ]]; then
    log "Already up to date — no update needed."
    exit 0
fi
log "New commits available: ${new_count}"

# ── 4. Capture old commit + changelog ────────────────────────────────────────

OLD_COMMIT=$(git rev-parse --short HEAD)
CHANGELOG=$(git log HEAD..origin/main --oneline --no-decorate | head -20)
log "Old commit: ${OLD_COMMIT}"
log "Incoming:"
echo "$CHANGELOG" | while read -r line; do log "  ${line}"; done

# ── 5. Pull ───────────────────────────────────────────────────────────────────

log "Pulling origin/main..."
git pull origin main 2>&1 | tee -a "$LOG_FILE"

NEW_COMMIT=$(git rev-parse --short HEAD)
log "New commit: ${NEW_COMMIT}"

# ── 6. Build ──────────────────────────────────────────────────────────────────

log "Building..."
if ! make build 2>&1 | tee -a "$LOG_FILE"; then
    log "ERROR: build failed — aborting update."
    # Notify Discord of build failure
    if [[ -n "${CHANDRA_DISCORD_BOT_TOKEN:-}" ]] && [[ -n "${CHANDRA_DISCORD_CHANNEL_ID:-}" ]]; then
        _discord_post "⚠️ **Auto-update failed** — build error on \`${NEW_COMMIT}\`. Check \`/tmp/chandrad-autoupdate.log\`."
    fi
    exit 1
fi
log "Build succeeded."

# ── 7. Test (hard gate) ───────────────────────────────────────────────────────

log "Running tests..."
if ! make test 2>&1 | tee -a "$LOG_FILE"; then
    log "ERROR: tests failed — aborting update. Rolling back git pull."
    git reset --hard "${OLD_COMMIT}" 2>&1 | tee -a "$LOG_FILE" || true
    if [[ -n "${CHANDRA_DISCORD_BOT_TOKEN:-}" ]] && [[ -n "${CHANDRA_DISCORD_CHANNEL_ID:-}" ]]; then
        _discord_post "⚠️ **Auto-update aborted** — tests failed on \`${NEW_COMMIT}\`. Reverted to \`${OLD_COMMIT}\`."
    fi
    exit 1
fi
log "Tests passed."

# ── Discord helper ────────────────────────────────────────────────────────────

_discord_post() {
    local msg="$1"
    curl -sf -X POST \
        "https://discord.com/api/v10/channels/${CHANDRA_DISCORD_CHANNEL_ID}/messages" \
        -H "Authorization: Bot ${CHANDRA_DISCORD_BOT_TOKEN}" \
        -H "Content-Type: application/json" \
        -d "{\"content\": $(printf '%s' "$msg" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))')}" \
        > /dev/null 2>&1 || true
}

# ── 8. Deploy ─────────────────────────────────────────────────────────────────

log "Deploying ${NEW_COMMIT}..."

# Export vars for chandrad-update to use for Discord notification
export CHANDRA_OLD_COMMIT="$OLD_COMMIT"
export CHANDRA_NEW_COMMIT="$NEW_COMMIT"
export CHANDRA_CHANGELOG="$CHANGELOG"

# chandrad-update runs the binary swap + health check + rollback if needed,
# then posts the Discord notification itself.
exec /usr/local/bin/chandrad-update "${REPO}/bin/chandrad"
