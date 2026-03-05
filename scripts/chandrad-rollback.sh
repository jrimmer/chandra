#!/usr/bin/env bash
# chandrad-rollback.sh — list or restore a specific archived version
#
# Usage:
#   chandrad-rollback.sh             List available versions
#   chandrad-rollback.sh <version>   Roll back to <version> (prefix match)
#   chandrad-rollback.sh --latest    Roll back to the most recent archive

set -euo pipefail

VERSION_DIR="${CHANDRA_VERSION_DIR:-/usr/local/lib/chandra/versions}"
PROD_BIN="${CHANDRA_BIN:-/usr/local/bin/chandrad}"
HEALTH_CMD="${CHANDRA_CLI:-/usr/local/bin/chandra}"
LOG_FILE="${CHANDRA_UPDATE_LOG:-/tmp/chandrad-update.log}"

log() { echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] $*" | tee -a "$LOG_FILE"; }

health_ok() { "$HEALTH_CMD" health 2>/dev/null | grep -qi "ok\|healthy\|running"; }

atomic_install() {
    local src="$1" dst="$2"
    local tmp="${dst}.tmp.$$"
    sudo install -m 755 "$src" "$tmp"
    sudo mv "$tmp" "$dst"
}

list_versions() {
    echo ""
    echo "Available versions (newest first):"
    echo "────────────────────────────────────────"

    if ! sudo ls -1t "$VERSION_DIR" 2>/dev/null | head -20 | while read -r v; do
        # Parse name: YYYYMMDD_HHMMSS_<commit>
        local ts="${v%%_*}"
        local rest="${v#*_}"
        local time_part="${rest%%_*}"
        local commit="${rest#*_}"
        local size
        size=$(sudo stat -c %s "${VERSION_DIR}/${v}" 2>/dev/null || echo "?")
        printf "  %-40s  %s %s  (%s bytes)\n" "$v" "${ts:0:4}-${ts:4:2}-${ts:6:2}" "${time_part//_/:}" "$size"
    done; then
        echo "  (no archived versions found in $VERSION_DIR)"
    fi
    echo ""
    echo "Current binary: $PROD_BIN"
    ls -la "$PROD_BIN" 2>/dev/null || echo "  (not found)"
    echo ""
    echo "Usage: chandrad-rollback.sh <version-prefix>"
    echo "       chandrad-rollback.sh --latest"
}

# ── main ─────────────────────────────────────────────────────────────────────

if [[ $# -eq 0 ]]; then
    list_versions
    exit 0
fi

TARGET="$1"

# Resolve version
MATCH=""
if [[ "$TARGET" == "--latest" ]]; then
    MATCH=$(sudo ls -1t "$VERSION_DIR" 2>/dev/null | head -1 || true)
    if [[ -z "$MATCH" ]]; then
        echo "ERROR: no archived versions found in $VERSION_DIR"
        exit 1
    fi
else
    # Prefix match
    MATCH=$(sudo ls -1t "$VERSION_DIR" 2>/dev/null | grep "^${TARGET}" | head -1 || true)
    if [[ -z "$MATCH" ]]; then
        echo "ERROR: no version matching '${TARGET}' found in $VERSION_DIR"
        echo ""
        list_versions
        exit 1
    fi
fi

VERSION_BIN="${VERSION_DIR}/${MATCH}"
echo ""
echo "Rolling back to: $MATCH"
echo "From: $VERSION_BIN"
echo ""

# Confirm (skip if running non-interactively)
if [[ -t 0 ]]; then
    read -rp "Proceed? [y/N] " confirm
    [[ "$confirm" =~ ^[Yy] ]] || { echo "Cancelled."; exit 0; }
fi

# Use chandrad-update to do the actual swap (gets health check + rollback safety)
log "chandrad-rollback: restoring $MATCH"
exec /usr/local/bin/chandrad-update "$VERSION_BIN"
