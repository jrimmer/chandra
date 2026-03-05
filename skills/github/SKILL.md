---
name: github
description: GitHub operations — issues, PRs, CI runs, code review, repository management via the gh CLI.
category: development
triggers: [github, gh, pull request, pr, issue, ci, workflow run, code review, git hub, open pr, merge pr, check ci]
requires:
  bins: [gh, git]
---

GitHub operations via the `gh` CLI. Auth is configured on the local system.

## Auth

```bash
gh auth status   # verify
gh auth login    # if needed
```

Token expires: 2026-04-25 (stored in TOOLS.md). Renew at https://github.com/settings/tokens/new with scopes: `repo`, `read:org`, `workflow`.

## Common operations

```bash
# Issues
gh issue list --repo jrimmer/chandra
gh issue view <number> --repo jrimmer/chandra
gh issue create --title "..." --body "..." --repo jrimmer/chandra

# PRs
gh pr list --repo jrimmer/chandra
gh pr view <number> --repo jrimmer/chandra
gh pr create --title "..." --body "..." --base main
gh pr merge <number> --squash

# CI / workflow runs
gh run list --repo jrimmer/chandra
gh run view <run-id> --repo jrimmer/chandra
gh run watch <run-id>

# Clone / repo info
gh repo view jrimmer/chandra
```

## For Chandra's own repo

The deploy key on chandra-test (`~/.ssh/id_ed25519`) has write access to `git@github.com:jrimmer/chandra.git`. Direct git push works without gh auth on the VM.

For issue management and CI, use `gh` from the OpenClaw host (where gh auth is configured), or via exec with `host=` pointing to the OpenClaw host.

## Notes

- Never force-push to main
- PRs should have passing tests before merge
- Issue labels: `bug`, `enhancement`, `documentation`, `performance`
