---
name: hetzner
description: Manage Hetzner Cloud infrastructure — servers, volumes, snapshots, firewalls. Monitor CPU/memory, create/delete servers, take snapshots, manage projects.
category: infrastructure
triggers: [hetzner, cloud server, vps, create server, delete server, server status, hetzner monitor, cloud infrastructure]
requires:
  bins: [ssh]
---

Hetzner Cloud management via the `hetzner` CLI (`~/bin/hetzner`) on the local machine (Sal's workstation/OpenClaw host).

## CLI

```bash
~/bin/hetzner <subcommand>
```

Or use the Hetzner API directly via curl with the project tokens.

## Projects and tokens

Tokens are stored in `~/.config/hetzner/tokens.json` (on the OpenClaw host, not chandra-test).

Projects:
- **Mirthworks**: servers `site` (cx33, nbg1), `wrdln` (cpx21, ash)
- **Sixtyfiveohtwo**: server `site` (cx33, nbg1)
- **Bezgelor**: server `granok` (cpx21, ash)
- **Tenzing**: server `hillary` (cpx31, ash)

## Common operations

```bash
# List all servers across all projects
~/bin/hetzner list

# Server status
~/bin/hetzner status <project>

# SSH into a server
ssh root@<server-ip>

# API call (replace PROJECT_TOKEN)
curl -H "Authorization: Bearer $TOKEN" https://api.hetzner.cloud/v1/servers
```

## Monitoring

CPU alerts fire to Discord #alerts when usage exceeds threshold. Check `~/clawd/skills/hetzner/` for the monitoring script.

## Notes

- Hetzner API is rate-limited; avoid rapid polling
- Servers use Hetzner private networks for internal comms where configured
- Snapshots are charged; don't create them unnecessarily
