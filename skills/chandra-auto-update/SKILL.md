---
name: chandra-auto-update
description: Automatically checks for and applies Chandra updates from the git repository on a schedule. Respects version pins — if a pin is set, reports it and skips. Runs tests before deploying and rolls back automatically if health check fails.
category: maintenance
cron:
  interval: 24h
  prompt: |
    Check if there's a new version of Chandra to deploy.

    ## Pin check
    First check if a version is pinned:
    ```
    exec: cat ~/.config/chandra/update.pin 2>/dev/null || echo "(no pin)"
    ```
    If the pin file exists and is non-empty, read its contents. Report the pinned version
    and skip the update. Do not proceed past this point if pinned.

    ## Current version check
    ```
    exec: cd ~/chandra && git rev-parse --short HEAD
    exec: cd ~/chandra && git log --oneline -1
    ```

    ## Fetch latest from remote
    ```
    exec: cd ~/chandra && git fetch origin main --quiet 2>&1
    ```

    ## Compare local vs remote
    ```
    exec: cd ~/chandra && git rev-list HEAD..origin/main --count
    ```
    If count is 0: report "Already up to date — no update needed." and stop.

    ## New commits available — pull, build, test, deploy
    Log what's new:
    ```
    exec: cd ~/chandra && git log HEAD..origin/main --oneline
    ```

    Pull:
    ```
    exec: cd ~/chandra && git pull origin main
    ```

    Build (confirmed — this is an expected automated operation):
    ```
    exec: cd ~/chandra && make build
    ```

    Test (hard gate — abort if this fails):
    ```
    exec: cd ~/chandra && make test
    ```
    If tests fail: report the failure, do NOT proceed with deployment.

    Deploy using the safe update script:
    ```
    exec: nohup /usr/local/bin/chandrad-update ~/chandra/bin/chandrad > /tmp/chandrad-update.log 2>&1 & disown; echo "update started"
    ```

    Report: "Auto-update started — I'll report back once the health check completes
    (check /tmp/chandrad-update-result and /tmp/chandrad-update.log for outcome)."

    After restart, on next interaction read the result:
    ```
    exec: cat /tmp/chandrad-update-result 2>/dev/null || echo "(result not yet written)"
    ```
requires:
  bins: [git, make]
---

## Pin management

To **pin** to the current version (preventing auto-update):
```bash
exec: git -C ~/chandra rev-parse --short HEAD > ~/.config/chandra/update.pin
exec: cat ~/.config/chandra/update.pin
```

To **pin to a specific commit or tag**:
```bash
exec: echo "abc1234" > ~/.config/chandra/update.pin
```

To **unpin** (re-enable auto-update):
```bash
exec: rm -f ~/.config/chandra/update.pin && echo "unpinned"
```

To check pin status:
```bash
exec: cat ~/.config/chandra/update.pin 2>/dev/null && echo "(pinned)" || echo "(no pin — auto-update enabled)"
```

## Rollback

To list available versions:
```bash
exec: /usr/local/bin/chandrad-rollback
```

To roll back to a specific version (use prefix from the list):
```bash
exec: /usr/local/bin/chandrad-rollback 20260305_234009
```

To roll back to the most recent previous version:
```bash
exec: /usr/local/bin/chandrad-rollback --latest
```

## Version archive

Archived versions are stored in `/usr/local/lib/chandra/versions/`.
The last 5 versions are kept (configurable via `CHANDRA_VERSION_KEEP`).
Archive name format: `YYYYMMDD_HHMMSS_<commit-or-tag>`.

## Notes

- Auto-update always runs `make test` first — broken code is never deployed
- If the new binary fails the 30s health check, chandrad-update rolls back automatically
- If pinned: the skill reports the pin status and takes no action
- The `update.pin` file path: `~/.config/chandra/update.pin`
- Schedule can be changed by editing the `cron.interval` in this SKILL.md frontmatter
