#!/usr/bin/env bash
# chandrad-update.sh — safe in-place update with version archive and automatic rollback
#
# Usage: chandrad-update.sh <new_binary_path>
#
# Environment overrides:
#   CHANDRA_BIN          Production binary path (default: /usr/local/bin/chandrad)
#   CHANDRA_CLI          CLI binary path (default: /usr/local/bin/chandra)
#   CHANDRA_VERSION_DIR  Version archive directory (default: /usr/local/lib/chandra/versions)
#   CHANDRA_VERSION_KEEP Number of versions to retain (default: 5)
#   CHANDRA_UPDATE_WAIT  Health poll timeout seconds (default: 30)
#   CHANDRA_UPDATE_LOG   Log file path (default: /tmp/chandrad-update.log)
#   CHANDRA_ALERT_WEBHOOK  Discord webhook URL for catastrophic failure alerts
#
# Workflow:
#   1. Validate new binary
#   2. Archive current version (timestamped, with commit hash)
#   3. Rotate old archives (keep last CHANDRA_VERSION_KEEP)
#   4. Atomic install (install + mv to avoid EBUSY on running binary)
#   5. Kill running daemon
#   6. Start new daemon
#   7. Poll health for up to CHANDRA_UPDATE_WAIT seconds
#   8. Success → write result file, exit 0
#   9. Failure → kill new daemon, restore most-recent archive, exit 1

set -euo pipefail

NEW_BIN="${1:?usage: chandrad-update.sh <new_binary_path>}"
PROD_BIN="${CHANDRA_BIN:-/usr/local/bin/chandrad}"
BACKUP_BIN="${PROD_BIN}.bak"   # kept for daemon.restart compatibility
VERSION_DIR="${CHANDRA_VERSION_DIR:-/usr/local/lib/chandra/versions}"
VERSION_KEEP="${CHANDRA_VERSION_KEEP:-5}"
HEALTH_CMD="${CHANDRA_CLI:-/usr/local/bin/chandra}"
MAX_WAIT="${CHANDRA_UPDATE_WAIT:-30}"
LOG_FILE="${CHANDRA_UPDATE_LOG:-/tmp/chandrad-update.log}"
RESULT_FILE="/tmp/chandrad-update-result"

# ── helpers ──────────────────────────────────────────────────────────────────

log() { echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] $*" | tee -a "$LOG_FILE"; }

health_ok() { "$HEALTH_CMD" health 2>/dev/null | grep -qi "ok\|healthy\|running"; }

# Post a message to Discord via the bot API.
# Requires CHANDRA_DISCORD_BOT_TOKEN and CHANDRA_DISCORD_CHANNEL_ID to be set.
discord_post() {
    local msg="$1"
    [[ -z "${CHANDRA_DISCORD_BOT_TOKEN:-}" ]] && return
    [[ -z "${CHANDRA_DISCORD_CHANNEL_ID:-}" ]] && return
    local payload
    payload=$(python3 -c "import json,sys; print(json.dumps({'content': sys.argv[1]}))" "$msg" 2>/dev/null || true)
    [[ -z "$payload" ]] && return
    curl -sf -X POST \
        "https://discord.com/api/v10/channels/${CHANDRA_DISCORD_CHANNEL_ID}/messages" \
        -H "Authorization: Bot ${CHANDRA_DISCORD_BOT_TOKEN}" \
        -H "Content-Type: application/json" \
        -d "$payload" > /dev/null 2>&1 || true
}

atomic_install() {
    local src="$1" dst="$2"
    local tmp="${dst}.tmp.$$"
    sudo install -m 755 "$src" "$tmp"
    sudo mv "$tmp" "$dst"
}

kill_daemon() {
    if pgrep -x chandrad > /dev/null 2>&1; then
        log "Stopping chandrad..."
        pkill -x chandrad 2>/dev/null || true
        for i in $(seq 1 10); do
            sleep 1
            if ! pgrep -x chandrad > /dev/null 2>&1; then
                log "Daemon stopped after ${i}s."
                return 0
            fi
        done
        log "Daemon still alive after 10s; sending SIGKILL"
        pkill -9 -x chandrad 2>/dev/null || true
        sleep 1
    else
        log "Daemon was not running."
    fi
}

start_daemon() {
    log "Starting daemon: $PROD_BIN"
    nohup "$PROD_BIN" >> "$LOG_FILE" 2>&1 &
    disown
}

# ── version archiving ────────────────────────────────────────────────────────

archive_current() {
    if [[ ! -f "$PROD_BIN" ]]; then
        log "WARNING: no existing binary to archive at $PROD_BIN"
        return
    fi

    sudo mkdir -p "$VERSION_DIR"

    # Build archive name: YYYYMMDD_HHMMSS_<commit>
    # Try to get commit hash from new binary's source repo context
    local commit="unknown"
    local chandra_repo
    chandra_repo="$(find /home/deploy /root -maxdepth 3 -name '.git' -type d 2>/dev/null | head -1 | xargs dirname 2>/dev/null || true)"
    if [[ -n "$chandra_repo" && -d "$chandra_repo/.git" ]]; then
        commit=$(git -C "$chandra_repo" rev-parse --short HEAD 2>/dev/null || echo "unknown")
        # Use tag name if HEAD is tagged
        local tag
        tag=$(git -C "$chandra_repo" describe --exact-match HEAD 2>/dev/null || true)
        if [[ -n "$tag" ]]; then
            commit="${tag}"
        fi
    fi

    local timestamp
    timestamp=$(date -u +%Y%m%d_%H%M%S)
    local archive_name="${timestamp}_${commit}"
    local archive_path="${VERSION_DIR}/${archive_name}"

    log "Archiving current binary → ${archive_path}"
    sudo install -m 755 "$PROD_BIN" "$archive_path"

    # Also update the .bak symlink for daemon.restart compatibility
    atomic_install "$PROD_BIN" "$BACKUP_BIN"

    # Rotate: keep only the last VERSION_KEEP versions
    local count
    count=$(sudo ls -1t "$VERSION_DIR" | wc -l)
    if (( count > VERSION_KEEP )); then
        local to_remove=$(( count - VERSION_KEEP ))
        sudo ls -1t "$VERSION_DIR" | tail -n "$to_remove" | while read -r old; do
            log "Rotating old version: $old"
            sudo rm -f "${VERSION_DIR}/${old}"
        done
    fi
}

rollback_to_latest_archive() {
    log "─── ROLLBACK INITIATED ───"
    kill_daemon

    # Find most recent archived version
    local latest
    latest=$(sudo ls -1t "$VERSION_DIR" 2>/dev/null | head -1 || true)

    if [[ -z "$latest" ]]; then
        # Fall back to .bak
        if [[ -f "$BACKUP_BIN" ]]; then
            log "No archive found; restoring .bak"
            atomic_install "$BACKUP_BIN" "$PROD_BIN"
        else
            log "🚨 CRITICAL: no archive or .bak found — manual intervention required."
            if [[ -n "${CHANDRA_ALERT_WEBHOOK:-}" ]]; then
                curl -sf -X POST "$CHANDRA_ALERT_WEBHOOK" \
                    -H "Content-Type: application/json" \
                    -d '{"content":"🚨 chandrad rollback failed — no archive or backup found. Manual intervention required."}' \
                    > /dev/null 2>&1 || true
            fi
            exit 1
        fi
    else
        log "Restoring archive: $latest"
        atomic_install "${VERSION_DIR}/${latest}" "$PROD_BIN"
    fi

    start_daemon
    sleep 3

    if health_ok; then
        log "✅ Rollback successful — previous version is running."
        discord_post "⚠️ **Chandra update rolled back** — \`${CHANDRA_NEW_COMMIT:-unknown}\` failed health check. Reverted to previous version."
    else
        log "🚨 CRITICAL: rollback daemon failed health check."
        log "   Archive directory: $VERSION_DIR"
        log "   Start manually: sudo $PROD_BIN"
        if [[ -n "${CHANDRA_ALERT_WEBHOOK:-}" ]]; then
            curl -sf -X POST "$CHANDRA_ALERT_WEBHOOK" \
                -H "Content-Type: application/json" \
                -d '{"content":"🚨 chandrad rollback daemon failed health check. Manual intervention required."}' \
                > /dev/null 2>&1 || true
        fi
    fi
    exit 1
}

# ── main ─────────────────────────────────────────────────────────────────────

echo "" >> "$LOG_FILE"
log "══════════════════════════════════════"
log "chandrad-update started"
log "  new binary   : $NEW_BIN"
log "  prod path    : $PROD_BIN"
log "  version dir  : $VERSION_DIR (keep=${VERSION_KEEP})"
log "══════════════════════════════════════"

# 1. Validate
if [[ ! -f "$NEW_BIN" ]]; then
    log "ERROR: new binary not found: $NEW_BIN"
    exit 1
fi
[[ -x "$NEW_BIN" ]] || chmod +x "$NEW_BIN"

# 2 & 3. Archive current + rotate
archive_current

# 4. Atomic install
log "Installing new binary → $PROD_BIN"
atomic_install "$NEW_BIN" "$PROD_BIN"

# 5. Kill old daemon
kill_daemon
sleep 1

# 6. Start new daemon
start_daemon

# 7. Health poll
log "Health polling (max ${MAX_WAIT}s)..."
HEALTHY=false
for i in $(seq 1 "$MAX_WAIT"); do
    sleep 1
    if health_ok; then
        log "✅ Health check passed after ${i}s."
        HEALTHY=true
        break
    fi
done

if [[ "$HEALTHY" == "true" ]]; then
    log "══════════════════════════════════════"
    log "✅ Update complete — new version running."
    log "══════════════════════════════════════"
    echo "update_ok:$(date -u +%Y-%m-%dT%H:%M:%SZ):$NEW_BIN" > "$RESULT_FILE"

    # Post Discord changelog notification.
    OLD="${CHANDRA_OLD_COMMIT:-unknown}"
    NEW="${CHANDRA_NEW_COMMIT:-unknown}"
    CHANGES="${CHANDRA_CHANGELOG:-}"
    if [[ -n "$CHANGES" ]]; then
        CHANGES_FMT=$(echo "$CHANGES" | awk '{print "• "$0}' | head -15 | tr '\n' '|' | sed 's/|/\n/g')
        MSG="🚀 **Chandra updated** \`${OLD}\` → \`${NEW}\`\n**What changed:**\n${CHANGES_FMT}"
    else
        MSG="🚀 **Chandra updated** \`${OLD}\` → \`${NEW}\`"
    fi
    discord_post "$MSG"
    exit 0
fi

# 8. Health failed — roll back
log "Health check failed after ${MAX_WAIT}s."
echo "update_failed:$(date -u +%Y-%m-%dT%H:%M:%SZ):rollback" > "$RESULT_FILE"
rollback_to_latest_archive
