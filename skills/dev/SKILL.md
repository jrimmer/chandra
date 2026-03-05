---
name: dev
description: Software development skill for the Chandra codebase. Knows the repo layout, build system, test commands, git workflow, and deployment process. Use when working on Chandra's own source code.
category: development
triggers: [build chandra, test chandra, deploy chandra, fix bug, commit, push, go build, go test, make build, restart daemon, install binary, git commit, git push, chandra source]
requires:
  bins: [git, go, make, ssh]
---

You are working on the Chandra codebase. Use `exec`, `read_file`, and `write_file` to make changes.

## Repo

- **Location on VM**: `~/chandra/` (as user `deploy` on `chandra-test`)
- **Remote**: `git@github.com:jrimmer/chandra.git` (branch: `main`)
- **SSH target**: `deploy@chandra-test` (or locally if already on the VM)

## Key directories

```
cmd/chandrad/main.go         — daemon entry point; tool registration, routing, handlers
cmd/chandra/commands.go      — CLI commands
internal/tools/              — built-in tools (schedule/, filesystem/, shell/, confirm/, sandbox/)
internal/channels/           — channel adapters (discord/, supervisor.go)
internal/memory/             — episodic/, semantic/, intent/, identity/
internal/skills/             — skill registry, parser, types
internal/provider/           — LLM providers (anthropic/, openai/, embedcache/, embeddings/)
internal/config/config.go    — config struct + validation
store/migrations/            — SQLite migrations (001–009)
skills/                      — built-in Go skills (web, homeassistant, context, mqtt)
tests/integration/           — integration tests
```

## Build

```bash
cd ~/chandra
make build
# or:
export PATH=$PATH:/usr/local/go/bin
CGO_ENABLED=1 go build -tags sqlite_fts5 -o bin/chandrad ./cmd/chandrad
CGO_ENABLED=1 go build -tags sqlite_fts5 -o bin/chandra ./cmd/chandra
```

**Important**: always use `-tags sqlite_fts5` — omitting it compiles but FTS5 queries fail at runtime.

## Test

```bash
cd ~/chandra && make test
# or: go test -tags sqlite_fts5 ./...
# race detector: go test -tags sqlite_fts5 -race ./...
```

## Install + deploy

```bash
# Stop daemon, install new binary, restart
pkill chandrad || true
sleep 1
sudo cp ~/chandra/bin/chandrad /usr/local/bin/chandrad
sudo cp ~/chandra/bin/chandra /usr/local/bin/chandra
chandrad > /tmp/chandrad.log 2>&1 &
sleep 2 && chandra health
```

## Self-restart via API (preferred over pkill)

After installing a new binary, call `daemon.restart` via the CLI:
```bash
chandra daemon restart   # if CLI command exists
# or via exec: the daemon.restart API endpoint re-execs the binary
```

## Git workflow

```bash
cd ~/chandra
git add -A
git commit -m "type(scope): description"
git push
```

Commit message types: `feat`, `fix`, `refactor`, `docs`, `test`, `perf`, `chore`

## Adding a new tool

1. Create `internal/tools/<package>/<name>_tool.go`
2. Implement `pkg.Tool` interface: `Definition() pkg.ToolDef` + `Execute(ctx, pkg.ToolCall) (pkg.ToolResult, error)`
3. Register in `cmd/chandrad/main.go`: `registry.Register(mypkg.NewMyTool())`
4. Build + test + deploy

## Adding a new skill

Skills live in `~/.config/chandra/skills/<name>/SKILL.md`. Write the SKILL.md with frontmatter, then:
```bash
chandra skill reload
chandra skill pending       # if generated (needs approval)
chandra skill approve <name>
```

## Database migrations

Add `store/migrations/NNN_description.up.sql`. The migration runs automatically at daemon startup. Never edit existing migrations — always add a new one.

## Config

- Config: `~/.config/chandra/config.toml` (0600)
- DB: `~/.config/chandra/chandra.db`
- Skills dir: `~/.config/chandra/skills/`
- Socket: `/run/user/1000/chandra/chandra.sock`

## Logs

```bash
tail -f /tmp/chandrad.log
# or: journalctl -u chandrad -f  (if running as systemd service)
```
