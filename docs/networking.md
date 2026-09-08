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

## nftables IP/MAC binding

Each VM gets a per-VM chain plus a shared platform chain, so large fleets do
not explode the rule set. The implementation uses `github.com/google/nftables`
(the iptables successor, programmed via netlink):

```text
table vps  (family ip)     base chain vps_platform  hook forward prio 0 policy accept
table vps6 (family ip6)    base chain vps_platform  hook forward prio 0 policy accept
vps_platform:  jump vps_vm_<id>                      (one jump per VM)
vps_vm_<id>:
  ether saddr <mac> ip saddr != <ip>   drop           (VM MAC with a foreign source IP)
  ip saddr <ip> ether saddr != <mac>   drop           (VM IP from a foreign MAC)
```

IPv4 and IPv6 rules live in separate `ip`/`ip6` family tables so the payload
expressions stay unambiguous. All mutations are idempotent: re-provisioning a
VM first removes any existing chain/jump and then recreates it, so rules are
never duplicated. Deleting a VM removes its jump rule and flushes/deletes its
chain. Rule construction is a pure function (`vmBindingRule`) so it is
unit-testable without touching the kernel.

The former `github.com/google/iptables` was archived upstream; nftables is the
successor and is what this project uses.

## DNS

DNS servers come from the network record and are written into the cloud-init
`network-config`.

## Console networking

- **VNC** is bound to `127.0.0.1` on the agent with an auto-allocated port and
  a password. The Control Plane issues a one-time token; the console gateway
  proxies a WebSocket to the VNC TCP port.
- **Serial** consoles use the domain's `<serial type='pty'>` device. The agent
  reports the PTY path from the running domain XML; the gateway proxies a
  WebSocket to the Unix domain socket.

Neither VNC nor the serial PTY is ever exposed publicly.