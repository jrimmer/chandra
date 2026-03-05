---
name: proxmox
description: Manage Proxmox VE — create/clone/start/stop VMs and LXC containers, snapshots, backups, storage, cluster health.
category: infrastructure
triggers: [proxmox, vm, virtual machine, lxc, container, proxmox ve, pve, create vm, start vm, stop vm, snapshot, backup, node status]
requires:
  bins: [ssh, curl]
---

Proxmox VE management via REST API or SSH.

## Access

- **Web UI**: https://10.1.0.10:8006
- **API token**: `root@pam!clawdhub=dcce2f14-3786-41f5-b0a0-9ea7bbc48b60`
- **SSH**: `ssh root@10.1.0.10`
- **Nodes**: vm1, vm2, vm3

## API calls

```bash
TOKEN="root@pam!clawdhub=dcce2f14-3786-41f5-b0a0-9ea7bbc48b60"

# List VMs across cluster
curl -sk -H "Authorization: PVEAPIToken=$TOKEN" \
  https://10.1.0.10:8006/api2/json/cluster/resources?type=vm

# Node status
curl -sk -H "Authorization: PVEAPIToken=$TOKEN" \
  https://10.1.0.10:8006/api2/json/nodes/vm1/status/current

# Start VM (node=vm1, vmid=106)
curl -sk -X POST -H "Authorization: PVEAPIToken=$TOKEN" \
  https://10.1.0.10:8006/api2/json/nodes/vm1/qemu/106/status/start

# Stop VM
curl -sk -X POST -H "Authorization: PVEAPIToken=$TOKEN" \
  https://10.1.0.10:8006/api2/json/nodes/vm1/qemu/106/status/stop

# Create snapshot
curl -sk -X POST -H "Authorization: PVEAPIToken=$TOKEN" \
  -d "snapname=auto-$(date +%Y%m%d)&description=auto snapshot" \
  https://10.1.0.10:8006/api2/json/nodes/vm1/qemu/106/snapshot
```

## Known VMs / LXC

- **VM 106**: `trina-oc` / Slay — `10.1.0.162` — OpenClaw instance (deploy user)
- **LXC 104**: `mission-control` — `10.1.0.109` — Node 22, port 3000

## SSH to Proxmox host

```bash
ssh root@10.1.0.10
# then: qm list, pct list, pvecm status
```

## Notes

- API token has full root access — use carefully
- Avoid stopping VMs without checking what's running on them
- Snapshots consume storage — clean up old ones
- Always verify node before destructive operations: vm1, vm2, or vm3
