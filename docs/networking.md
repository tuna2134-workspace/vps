# Networking

## Networks and bridges

Each network definition specifies the Linux bridge that VM NICs attach to:

```text
Network
├── ID
├── Name
├── Bridge Name      e.g. br0, br-public, br-private, br-storage
├── IPv4 configuration
├── IPv6 configuration
└── Status
```

A VM NIC is rendered as a bridge interface:

```xml
<interface type='bridge'>
  <mac address='02:...'/>
  <source bridge='br-public'/>
  <model type='virtio'/>
</interface>
```

Domain XML is generated with the official `libvirt.org/go/libvirtxml`
bindings (see `internal/agent/libvirt/domain.go`).

## IPAM

- `ip_pools` hold a CIDR + gateway per network.
- `ip_allocations` record which VM holds which address.
- Allocation runs inside a transaction with a row lock on the pool; the next
  free address is chosen and inserted. A partial unique index on
  `(pool_id, ip_address) WHERE status = 'allocated'` makes double-allocation
  impossible even under concurrency (verified by the integration test
  `TestIPAMConcurrentAllocation`).
- Network address, gateway, and broadcast are never handed out.

## MAC addresses

MACs are locally-administered (unicast bit set via the LAA bit 0x02, multicast
bit cleared). Random draws are checked against the DB and the `UNIQUE(mac_address)`
constraint backs the guarantee. See `internal/controlplane/macalloc`.

## iptables IP/MAC binding

Each VM gets a per-VM chain plus one shared platform chain, so large fleets do
not explode the FORWARD chain:

```text
FORWARD          -j VPS_PLATFORM              (created once)
VPS_PLATFORM     -j VPS_VM_<id>               (one jump per VM)
VPS_VM_<id>:
  -s ! <ip> -m mac --mac-source <mac> -j DROP   (VM MAC with foreign source IP)
  -s <ip>  -m mac ! --mac-source <mac> -j DROP  (VM IP from a foreign MAC)
```

Rules are applied to both IPv4 and IPv6 tables. All mutations are idempotent:
`Exists` is checked before `Append`, so re-provisioning a VM never duplicates
rules. Deleting a VM flushes and removes its chain and the platform jump.
Rule generation is a pure function (`vmBindingRules`) so it is unit-testable
without kernel access.

The implementation uses `github.com/coreos/go-iptables`. The former
`github.com/google/iptables` module was removed upstream; `coreos/go-iptables`
is the maintained continuation of the same project.

## DNS

DNS servers come from the network record and are written into the cloud-init
`network-config`.

## Console networking

VNC is bound to `127.0.0.1` on the agent with an auto-allocated port and a
password. The Control Plane issues a one-time token; the console gateway
proxies a WebSocket to the VNC port. VNC is never exposed publicly.