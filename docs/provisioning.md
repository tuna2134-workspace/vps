# VM Provisioning

This document describes how a VM is provisioned end to end, and the failure
handling that prevents orphaned resources.

## Flow

```text
 1. User requests VPS              POST /v1/vms (idempotency key)
 2. Control Plane validates request
 3. Billing status check           active subscription required
 4. Scheduler selects Agent       CPU/memory/storage/health scoring
 5. Allocate VM record            instance_id, MAC (DB-unique)
 6. Allocate IP address           transactional IPAM (no double alloc)
 7. Create provisioning operation pending -> running
 8. Control Plane calls Agent via gRPC  AgentService.CreateVM
 9. Agent fetches image           HTTP(S)/file://, checksum + size verified
10. Agent creates root disk volume (qcow2)
11. Agent uploads image into volume, resizes to plan disk size
12. Agent generates cloud-init ISO (user-data, meta-data, network-config)
13. Agent uploads ISO as a CD-ROM volume
14. Agent configures nftables IP/MAC binding (idempotent chains)
15. Agent builds domain XML (official libvirt-go-xml) with bridge NIC
16. Agent defines the domain
17. Agent starts the domain
18. Agent reports result
19. Control Plane updates operation -> succeeded, VM -> running
```

## Failure handling

- If the agent returns a failed operation, the Control Plane marks the
  operation `failed` and the VM `error`. IP allocations and the VM record are
  retained so an operator can retry or clean up.
- The agent cleans up its own partial state before retrying: any leftover
  root/cloud-init volumes are deleted at the start of `CreateVM`, and the
  temporary image/ISO files are removed after upload. Cleanup failures are
  logged, never silently swallowed.
- If the gRPC call itself fails (agent unreachable), the operation is marked
  `failed` with `AGENT_UNAVAILABLE`; the worker does not auto-retry to avoid
  double-provisioning, but the idempotency key makes a manual retry safe.

## Scheduler

The scheduler (`internal/controlplane/scheduler`) considers:

- CPU capacity and usage
- Memory capacity and usage (rejects placements that would overcommit)
- Storage capacity
- Node health (prefers `healthy` over `degraded`)

It is deliberately decoupled from the gRPC layer and from specific agent
endpoints. Future policies (affinity, anti-affinity, geographic location,
storage performance, overcommit ratios) extend the scoring function without
changing the placement API.

## Images

Images are records in the `images` table:

```text
Image ID, Name, Version, Architecture, Format,
Source URL, Checksum, Size, Cloud-init compatible
```

The Control Plane sends the image reference in `CreateVMRequest.ImageRef`; the
agent fetches it directly (HTTP/HTTPS, `file://`, or a local path). The
Control Plane never relays disk bytes over gRPC. Future backends (S3-compatible
object storage) plug into the agent's `image.Fetcher`.

## Cloud-init

The agent generates a NoCloud seed ISO with `go-diskfs` containing:

- `meta-data` — `instance-id`, `local-hostname`
- `user-data` — user, optional password, SSH keys
- `network-config` — Network Config Version 2 (netplan)

The network config is generated from the IPAM allocation:

```yaml
version: 2
ethernets:
  eth0:
    addresses:
      - 192.0.2.10/24
    routes:
      - to: default
        via: 192.0.2.1
    nameservers:
      addresses:
        - 1.1.1.1
```

IPv6 static addressing and DNS are supported the same way.

## Operations

All VM mutations are asynchronous operations persisted in `vm_operations`.
Each operation carries a unique `idempotency_key`, so the same request can be
replayed safely. Lifecycle transitions are validated: you cannot stop a VM that
is not running, etc.

## Console

`POST /v1/vms/{id}/console` (VM must be running) issues a single-use, expiring
token. Clients connect to the console gateway WebSocket; the gateway validates
the token and bridges to the VM's console endpoint.

Two console types are supported:

- **VNC** (`console_type: "vnc"`) — graphical console for noVNC. The VNC
  listener is bound to loopback on the agent (auto-allocated port + password)
  and is never published to the internet. The gateway bridges the WebSocket to
  the TCP endpoint.
- **Serial** (`console_type: "serial"`) — text console over the domain's serial
  PTY. The agent reports the PTY path (`/dev/pts/N`, parsed from the running
  domain XML via the official libvirt-go-xml bindings); the gateway bridges the
  WebSocket to the Unix domain socket. The PTY path is never exposed to the
  client.