#!/usr/bin/env bash
# chandrad-update.sh — safe in-place update with automatic rollback
#
# Usage: chandrad-update.sh <new_binary_path>
#
# Workflow:
#   1. Validate new binary
#   2. Backup current binary → chandrad.bak
#   3. Install new binary
#   4. Kill running daemon (if any)
#   5. Start new daemon
#   6. Poll health for up to MAX_WAIT seconds
#   7. If healthy → report success
#   8. If unhealthy → kill new daemon, restore backup, restart old, report failure
#
# All output is written to LOG_FILE, which the new daemon can read on first
# interaction to report the update result.

set -euo pipefail

NEW_BIN="${1:?usage: chandrad-update.sh <new_binary_path>}"
PROD_BIN="${CHANDRA_BIN:-/usr/local/bin/chandrad}"
BACKUP_BIN="${PROD_BIN}.bak"
HEALTH_CMD="${CHANDRA_CLI:-/usr/local/bin/chandra}"
MAX_WAIT="${CHANDRA_UPDATE_WAIT:-30}"
LOG_FILE="${CHANDRA_UPDATE_LOG:-/tmp/chandrad-update.log}"

# ── helpers ─────────────────────────────────────────────────────────────────

log() { echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] $*" | tee -a "$LOG_FILE"; }

health_ok() { "$HEALTH_CMD" health 2>/dev/null | grep -qi "ok\|healthy\|running"; }

kill_daemon() {
    if pgrep -x chandrad > /dev/null 2>&1; then
        log "Stopping chandrad..."
        pkill -x chandrad 2>/dev/null || true
        # Wait up to 10s for it to stop
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
        log "Daemon not running."
    fi
}

start_daemon() {
    local binary="$1"
    log "Starting daemon: $binary"
    nohup "$binary" >> "$LOG_FILE" 2>&1 &
    disown
}

rollback() {
    log "─── ROLLBACK INITIATED ───"
    kill_daemon
    if [[ -f "$BACKUP_BIN" ]]; then
        log "Restoring backup: $BACKUP_BIN → $PROD_BIN"
        ROLLBACK_TMP="${PROD_BIN}.rbk.$$"
        sudo install -m 755 "$BACKUP_BIN" "$ROLLBACK_TMP"
        sudo mv "$ROLLBACK_TMP" "$PROD_BIN"
        start_daemon "$PROD_BIN"
        sleep 3
        if health_ok; then
            log "✅ Rollback successful — previous version is running."
        else
            log "🚨 CRITICAL: rollback daemon failed health check."
            log "   Manual recovery required."
            log "   Backup binary: $BACKUP_BIN"
            log "   Start manually: sudo $PROD_BIN"
            # Attempt direct Discord notification via webhook if configured
            if [[ -n "${CHANDRA_ALERT_WEBHOOK:-}" ]]; then
                curl -sf -X POST "$CHANDRA_ALERT_WEBHOOK" \
                    -H "Content-Type: application/json" \
                    -d "{\"content\":\"🚨 chandrad rollback failed — manual intervention required. Backup at \`$BACKUP_BIN\`\"}" \
                    > /dev/null 2>&1 || true
            fi
        fi
    else
        log "🚨 CRITICAL: no backup found at $BACKUP_BIN — cannot roll back."
        log "   Reinstall chandrad manually."
    fi
    exit 1
}

# ── main ─────────────────────────────────────────────────────────────────────

# Rotate log so fresh run is easy to read
echo "" >> "$LOG_FILE"
log "══════════════════════════════════════"
log "chandrad-update started"
log "  new binary : $NEW_BIN"
log "  prod path  : $PROD_BIN"
log "  backup     : $BACKUP_BIN"
log "══════════════════════════════════════"

# 1. Validate new binary
if [[ ! -f "$NEW_BIN" ]]; then
    log "ERROR: new binary not found: $NEW_BIN"
    exit 1
fi
if [[ ! -x "$NEW_BIN" ]]; then
    log "Making new binary executable"
    chmod +x "$NEW_BIN"
fi

# Quick sanity check — run --version or --help; if it segfaults, abort early
if ! "$NEW_BIN" --help > /dev/null 2>&1 && ! "$NEW_BIN" version > /dev/null 2>&1; then
    log "WARNING: new binary failed --help/version check (may be OK, continuing)"
fi

# 2. Backup current binary
if [[ -f "$PROD_BIN" ]]; then
    log "Backing up $PROD_BIN → $BACKUP_BIN"
    # Use install (not cp) to avoid "Text file busy" on running binary
    sudo install -m 755 "$PROD_BIN" "$BACKUP_BIN"
else
    log "WARNING: no existing binary at $PROD_BIN — no backup created"
fi

# 3. Install new binary
# On Linux, cp over a running binary causes "Text file busy".
# Use install to a temp path then mv (atomic rename) to swap the inode.
log "Installing new binary → $PROD_BIN"
PROD_TMP="${PROD_BIN}.tmp.$$"
sudo install -m 755 "$NEW_BIN" "$PROD_TMP"
sudo mv "$PROD_TMP" "$PROD_BIN"

# 4. Kill old daemon
kill_daemon

# Brief pause to let OS settle
sleep 1

# 5. Start new daemon
start_daemon "$PROD_BIN"

# 6. Health poll
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
    # Write a status file the new daemon can surface to the user
    echo "update_ok:$(date -u +%Y-%m-%dT%H:%M:%SZ):$NEW_BIN" > /tmp/chandrad-update-result
    exit 0
fi

# 7. Health check failed — roll back
log "Health check failed after ${MAX_WAIT}s."
echo "update_failed:$(date -u +%Y-%m-%dT%H:%M:%SZ):rollback" > /tmp/chandrad-update-result
rollback
