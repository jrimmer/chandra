---
name: chandra-auto-update
description: Autonomously polls for new Chandra releases on a schedule, builds, tests, and deploys them. Posts a changelog to Discord on success or failure. Respects a version pin file. No human approval required — runs silently and reports results directly to the channel.
category: maintenance
cron:
  interval: 24h
  prompt: |
    Run the Chandra auto-update check. This is a fully autonomous operation.

    Execute the autoupdate script with Discord credentials:

    ```
    exec(
      command="CHANDRA_DISCORD_BOT_TOKEN=$(grep bot_token ~/.config/chandra/config.toml | head -1 | sed 's/.*= \"//;s/\"//') CHANDRA_DISCORD_CHANNEL_ID=1478847178058367088 nohup /usr/local/bin/chandrad-autoupdate > /tmp/chandrad-autoupdate.log 2>&1 & disown; echo started",
      confirmed=true
    )
    ```

    Do not ask for approval. Do not send a message to the channel.
    The update script handles everything including the Discord notification.
    Respond with exactly: QUIET
requires:
  bins: [git, make, curl, python3]
---

## How it works

The `chandra-auto-update` skill runs on a 24-hour schedule. When it fires:

1. **Pin check** — if `~/.config/chandra/update.pin` exists and is non-empty, skip entirely
2. **Fetch** — `git fetch origin main`
3. **Count new commits** — if 0, exit silently
4. **Changelog** — capture `git log HEAD..origin/main --oneline`
5. **Pull** — `git pull origin main`
6. **Build** — `make build` (abort + notify Discord if fails)
7. **Test** — `make test` hard gate (abort + revert pull + notify Discord if fails)
8. **Deploy** — `chandrad-update` handles: archive current → install new → kill old → start new → 30s health poll → rollback if unhealthy
9. **Notify** — Discord message posted by `chandrad-update` on success or rollback

## Discord notification format

On success:
```
🚀 Chandra updated `abc1234` → `def5678`
What changed:
• feat(something): description
• fix(other): description
```

On rollback:
```
⚠️ Chandra update rolled back — `def5678` failed health check. Reverted to previous version.
```

On build/test failure:
```
⚠️ Auto-update failed — build error on `def5678`. Check /tmp/chandrad-autoupdate.log.
```

## Pin management

**Pin current version** (stop auto-updates):
```bash
exec: git -C ~/chandra rev-parse --short HEAD > ~/.config/chandra/update.pin && echo "pinned"
```

**Unpin** (re-enable auto-updates):
```bash
exec: rm -f ~/.config/chandra/update.pin && echo "unpinned"
```

**Check pin status**:
```bash
exec: cat ~/.config/chandra/update.pin 2>/dev/null && echo "(pinned)" || echo "(no pin — auto-update enabled)"
```

## Manual trigger

To run the auto-update check immediately:
```bash
exec: CHANDRA_DISCORD_BOT_TOKEN=<token> CHANDRA_DISCORD_CHANNEL_ID=1478847178058367088 /usr/local/bin/chandrad-autoupdate
```

Or just ask Chandra: "check for updates and deploy if there are any"

## Logs

```bash
exec: tail -30 /tmp/chandrad-autoupdate.log    # auto-update wrapper log
exec: tail -30 /tmp/chandrad-update.log         # binary swap + health check log
exec: cat /tmp/chandrad-update-result           # last update outcome
```
