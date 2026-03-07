---
name: chandra-auto-update
description: Autonomously polls for new Chandra releases on a schedule, builds, tests, and deploys them. Posts a changelog to Discord on success or failure. Respects a version pin file. No human approval required — runs silently and reports results directly to the channel.
category: maintenance
cron:
  interval: 24h
  prompt: |
    Run the Chandra auto-update script now:

    exec(
      command="CHANDRA_DISCORD_BOT_TOKEN=$(grep bot_token ~/.config/chandra/config.toml | head -1 | sed 's/.*= \"//;s/\"//') CHANDRA_DISCORD_CHANNEL_ID=1478847178058367088 /usr/local/bin/chandrad-autoupdate >> /tmp/chandrad-autoupdate.log 2>&1; echo done",
      confirmed=true
    )

    The script handles everything: pin check, git fetch, build, test, deploy, Discord notification.
    Do NOT investigate the repo state yourself. Do NOT send any message.
    Your entire response must be exactly: QUIET
requires:
  bins: [git, make, curl, python3]
---

## How it works

The `chandra-auto-update` skill runs on a 24-hour schedule. When it fires:

1. **Pin check** — if `~/.config/chandra/update.pin` exists and is non-empty, skip entirely (silently)
2. **Fetch** — `git fetch origin main`
3. **Count new commits** — if 0, exit silently
4. **Changelog** — capture `git log HEAD..origin/main --oneline`
5. **Pull** — `git pull origin main`
6. **Build** — `make build` (abort + notify Discord if fails)
7. **Test** — `make test` hard gate (abort + revert pull + notify Discord if fails)
8. **Deploy** — `chandrad-update` handles: archive → install → kill → start → health poll → rollback
9. **Notify** — Discord message posted by `chandrad-update` on success or rollback

The script is fully self-contained. Chandra's only job is to run it and say QUIET.

## Logs

```bash
exec: tail -30 /tmp/chandrad-autoupdate.log    # wrapper log
exec: tail -30 /tmp/chandrad-update.log         # binary swap log
exec: cat /tmp/chandrad-update-result           # last update outcome
```

## Pin management

Pin: `echo $(git -C ~/chandra rev-parse --short HEAD) > ~/.config/chandra/update.pin`
Unpin: `rm -f ~/.config/chandra/update.pin`
