# Architecture

The platform is split into two long-lived components that communicate over
gRPC (optionally mTLS):

```text
                    ┌─────────────────────┐
                    │    Control Plane    │
                    │                     │
                    │ - REST API          │
                    │ - Authentication    │
                    │ - User Management   │
                    │ - Billing           │
                    │ - Stripe            │
                    │ - VM Scheduler      │
                    │ - Cluster Metadata  │
                    │ - PostgreSQL        │
                    └──────────┬──────────┘
                               │
                            gRPC/mTLS
                               │
              ┌────────────────┼────────────────┐
              ▼                ▼                ▼
       ┌────────────┐    ┌────────────┐   ┌────────────┐
       │   Agent    │    │   Agent    │   │   Agent    │
       │ libvirt    │    │ libvirt    │   │ libvirt    │
       │ iptables   │    │ iptables   │   │ iptables   │
       │ cloud-init │    │ cloud-init │   │ cloud-init │
       └────────────┘    └────────────┘   └────────────┘
```

## Components

### Control Plane (`cmd/control-plane`)

Stateful service. Owns all durable metadata in PostgreSQL and exposes the
public REST API. Responsibilities:

- User registration, login, session management (opaque tokens, hashed at rest)
- RBAC (user / support / admin) enforced centrally in middleware
- Plans with version history
- Networks, IP pools, IPAM, MAC allocation
- VM records and asynchronous operations
- Scheduler (placement)
- Billing (Stripe: customers, subscriptions, invoices, payment intents,
  webhooks with signature verification and idempotent processing)
- Console token issuance and the WebSocket gateway for noVNC
- Cluster / node metadata and heartbeat reconciliation

The Control Plane **never** talks to libvirt directly; all hypervisor actions
are delegated to an Agent over gRPC.

### Agent (`cmd/agent`)

Stateless worker per compute node. Listens for gRPC `AgentService` calls and
performs the actual virtualization work on the host:

- VM lifecycle (define, start, stop, force-stop, reboot, destroy, undefine)
- Storage volumes via libvirt
- cloud-init NoCloud seed disk generation (`go-diskfs`)
- Image fetch + checksum verification
- iptables IP/MAC anti-spoofing binding
- Domain XML generation (official `libvirt.org/go/libvirtxml`)
- VNC endpoint exposure (loopback only) for the console gateway

The agent never stores state; it is a pure command executor. This makes nodes
replaceable and lets the Control Plane drive reconciliation.

## Data flow (VM creation)

1. `POST /v1/vms` (authenticated)
2. Control Plane validates request + billing gate
3. Scheduler selects a node (CPU/memory/storage/health)
4. VM record is allocated (MAC via the DB-unique generator)
5. IP is allocated transactionally (no double-allocation under concurrency)
6. A `create` operation is persisted with an idempotency key
7. A worker calls `AgentService.CreateVM` over gRPC
8. Agent fetches the image, creates volumes, generates + uploads the
   cloud-init ISO, configures iptables, defines and starts the domain
9. Control Plane marks the operation succeeded and the VM `running`

## Cross-cutting concerns

- **Observability**: structured JSON logs, `/healthz`, `/readyz`
- **Concurrency**: IP/MAC allocation use transactions + unique indexes;
  idempotency keys make retries safe
- **Security**: Argon2id passwords, hashed session tokens, generic login
  errors (no enumeration), rate limiting, PII isolation, signature-verified
  webhooks
- **Cleanup**: agents remove temp files, volumes, and iptables rules on
  failure/teardown

## Directory layout

```text
cmd/control-plane            Control Plane entrypoint
cmd/agent                    Agent entrypoint
internal/controlplane/api    REST handlers, middleware, router
internal/controlplane/auth   Argon2id password hashing
internal/controlplane/session opaque session tokens
internal/controlplane/rbac   role checks
internal/controlplane/users  registration / login / sessions
internal/controlplane/cluster cluster + node + heartbeat
internal/controlplane/plans  plan management
internal/controlplane/networks networks + IPAM
internal/controlplane/vms    VM service + provisioning worker
internal/controlplane/scheduler placement
internal/controlplane/billing Stripe integration
internal/controlplane/console console tokens + WebSocket gateway
internal/controlplane/grpcclient gRPC client to agents
internal/controlplane/agents gRPC connection factory
internal/controlplane/repositories repository layer
internal/agent/libvirt        libvirt adapter (official bindings)
internal/agent/libvirt/*.go   domain XML via libvirt-go-xml
internal/agent/cloudinit      NoCloud seed generation
internal/agent/image          image fetch + checksum
internal/agent/network        iptables IP/MAC binding
internal/agent/storage        storage volume abstraction
internal/agent/grpcserver     AgentService implementation
migrations/                   SQL migrations (embedded)
proto/                        Protocol Buffers (agent + common)
api/openapi/openapi.yaml      OpenAPI 3.x contract
```