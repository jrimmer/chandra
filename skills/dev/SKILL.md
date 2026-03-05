---
name: dev
description: Software development skill for the Chandra codebase. Knows the repo layout, build system, test commands, git workflow, and safe deployment process including rollback. Use when working on Chandra's own source code.
category: development
triggers: [build chandra, test chandra, deploy chandra, fix bug, commit, push, go build, go test, make build, restart daemon, install binary, git commit, git push, chandra source, update chandra, rollback]
requires:
  bins: [git, go, make, ssh]
---

You are working on the Chandra codebase. Use `exec`, `read_file`, and `write_file` to make changes.

## Repo

- **Location on VM**: `~/chandra/` (as user `deploy` on `chandra-test`)
- **Remote**: `git@github.com:jrimmer/chandra.git` (branch: `main`)
- **SSH target for remote ops**: `deploy@chandra-test`

## Key directories

```
cmd/chandrad/main.go         — daemon entry point; tool registration, routing, handlers
cmd/chandra/commands.go      — CLI commands
internal/tools/              — built-in tools:
  filesystem/                  read_file_tool.go, write_file_tool.go
  shell/                       exec_tool.go
  schedule/                    schedule_reminder_tool.go, list_intents_tool.go
  confirm/                     checkpoint tools
  sandbox/                     sandbox tools
internal/channels/           — channel adapters (discord/, supervisor.go)
internal/memory/             — episodic/, semantic/, intent/, identity/
internal/skills/             — skill registry, parser, types
internal/provider/           — LLM providers (anthropic/, openai/, embedcache/, embeddings/)
internal/config/config.go    — config struct + validation
store/migrations/            — SQLite migrations (001–009, always add new, never edit existing)
skills/                      — built-in Go skills (web, homeassistant, context, mqtt)
tests/integration/           — integration test suite
scripts/                     — operational scripts (chandrad-update.sh)
```

## Build

```bash
cd ~/chandra && make build
# or explicitly:
CGO_ENABLED=1 go build -tags sqlite_fts5 -o bin/chandrad ./cmd/chandrad
CGO_ENABLED=1 go build -tags sqlite_fts5 -o bin/chandra ./cmd/chandra
```

**Critical**: always use `-tags sqlite_fts5` — omitting it compiles but FTS5 queries silently fail at runtime.

Build output goes to `bin/` — never build directly to `/usr/local/bin/`.

## Test

```bash
cd ~/chandra && make test
# equivalent: go test -tags sqlite_fts5 ./...
# with race detector: go test -tags sqlite_fts5 -race ./...
```

Tests must pass (exit 0) before any deployment. This is a hard gate.

## Safe deployment — ALWAYS use chandrad-update

Never manually copy binaries and restart. Always use the update script which handles backup, health verification, and automatic rollback:

```bash
# 1. Build
cd ~/chandra && make build

# 2. Test (hard gate — do not proceed if this fails)
make test

# 3. Launch update script in background BEFORE anything dies
nohup /usr/local/bin/chandrad-update ~/chandra/bin/chandrad \
    > /tmp/chandrad-update.log 2>&1 & disown

# 4. Tell the user: "Update started — I'll be right back in ~30s"
# The script handles everything. Do NOT call daemon.restart after this.
```

The `chandrad-update` script:
1. Backs up current binary → `/usr/local/bin/chandrad.bak`
2. Installs new binary → `/usr/local/bin/chandrad`
3. Kills the running daemon
4. Starts new daemon
5. Polls `chandra health` for up to 30 seconds
6. **If healthy** → writes `update_ok` to `/tmp/chandrad-update-result`, exits 0
7. **If unhealthy** → kills new daemon, restores `.bak`, restarts old version, exits 1

After the new daemon starts, read the update result:
```bash
cat /tmp/chandrad-update-result
cat /tmp/chandrad-update.log | tail -20
```

## Rollback (manual)

If an update goes wrong and the script didn't auto-rollback:

```bash
pkill chandrad || true
sleep 1
sudo cp /usr/local/bin/chandrad.bak /usr/local/bin/chandrad
chandrad > /tmp/chandrad.log 2>&1 &
sleep 3 && chandra health
```

## daemon.restart (config-only restarts)

Use `daemon.restart` API only for config-reload restarts (no binary change):

```bash
# Via CLI if wired, or: exec the chandra CLI
chandra daemon restart
```

It backs up the binary before re-execing. **Do not use for code deployments** — use chandrad-update instead.

## Git workflow

```bash
cd ~/chandra
git add -A
git commit -m "type(scope): description"
git push
```

Types: `feat`, `fix`, `refactor`, `docs`, `test`, `perf`, `chore`

Always commit after a successful deployment so the repo reflects what's running.

## Adding a new tool

1. Create `internal/tools/<package>/<name>_tool.go`
2. Implement `pkg.Tool`: `Definition() pkg.ToolDef` + `Execute(ctx, pkg.ToolCall) (pkg.ToolResult, error)`
3. Register in `cmd/chandrad/main.go`: `registry.Register(mypkg.NewMyTool())`
4. Build → test → deploy via chandrad-update

## Adding a new skill

Skills live in `~/.config/chandra/skills/<name>/SKILL.md`:
```bash
# Write the skill (confirmed=true for .md is optional — .md doesn't need it)
write_file path="~/.config/chandra/skills/<name>/SKILL.md" content="..."

# Also write to repo for version control
write_file path="~/chandra/skills/<name>/SKILL.md" content="..."

# Reload registry
exec "chandra skill reload"

# If LLM-generated, it'll be pending — approve it
exec "chandra skill pending"
exec "chandra skill approve <name>"
```

## Database migrations

Add `store/migrations/NNN_description.up.sql`. Migration runs automatically at startup. **Never edit existing migrations** — always add a new numbered file.

## Config

- Config: `~/.config/chandra/config.toml` (0600)
- DB: `~/.config/chandra/chandra.db`
- Skills dir: `~/.config/chandra/skills/`
- Socket: `/run/user/1000/chandra/chandra.sock`

## Logs

```bash
tail -f /tmp/chandrad.log
tail -f /tmp/chandrad-update.log   # update progress and rollback events
cat /tmp/chandrad-update-result    # last update outcome
```
