#!/usr/bin/env bash
# chandrad-config-apply — apply a pending cold config change.
# Restarts the daemon, polls for health, auto-restores config.toml.bak on failure.
set -euo pipefail

CONFIG_PATH="${HOME}/.config/chandra/config.toml"
CONFIG_BAK="${CONFIG_PATH}.bak"
BINARY="${CHANDRA_BINARY:-/usr/local/bin/chandrad}"
LOG="${CHANDRA_CONFIG_APPLY_LOG:-/tmp/chandrad-config-apply.log}"

log() { echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] $*" | tee -a "$LOG"; }

log "chandrad-config-apply: starting"

# Restart daemon via chandrad-update.
/usr/local/bin/chandrad-update "$BINARY" || true

# Poll for daemon health for 30s.
for i in $(seq 1 6); do
    sleep 5
    if pgrep -x chandrad > /dev/null 2>&1; then
        log "chandrad-config-apply: daemon healthy after config change"
        exit 0
    fi
    log "chandrad-config-apply: waiting for daemon (attempt $i/6)..."
done

# Daemon failed to start — restore backup config.
log "chandrad-config-apply: daemon unhealthy, restoring backup config"
if [[ -f "$CONFIG_BAK" ]]; then
    cp "$CONFIG_BAK" "$CONFIG_PATH"
    log "chandrad-config-apply: restored config.toml.bak, restarting"
    /usr/local/bin/chandrad-update "$BINARY" || true
else
    log "chandrad-config-apply: no backup found at $CONFIG_BAK — manual intervention needed"
fi
exit 1
