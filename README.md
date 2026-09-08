# VPS Hosting Platform

A production-oriented, VirtFusion-style **VPS hosting backend** built in Go:
Control Plane + per-node Agents, PostgreSQL, gRPC (mTLS), libvirt, iptables,
cloud-init, and Stripe billing. **No UI** — the REST API and OpenAPI contract
are the deliverables a future frontend implements against.

## Overview

- **Control Plane** — REST API, authentication/sessions, RBAC, user & PII
  management, plans, networks, IPAM, MAC allocation, VM scheduling, async
  operations, Stripe billing, console token issuance.
- **Agent** — runs on each compute node; manages VMs through the official
  libvirt Go bindings, generates cloud-init NoCloud disks with `go-diskfs`,
  and enforces IP/MAC anti-spoofing with `go-iptables` (chain-based and
  idempotent).

The Control Plane never touches libvirt directly. All hypervisor work flows
through gRPC:

```text
Control Plane  --gRPC/mTLS-->  Agent  -->  libvirt / iptables / cloud-init
```

## Architecture & Docs

- [Architecture](docs/architecture.md)
- [API guide](docs/api.md) and the full [OpenAPI 3.x spec](api/openapi/openapi.yaml)
- [Provisioning flow](docs/provisioning.md)
- [Networking / IPAM / iptables](docs/networking.md)
- [Billing](docs/billing.md)

## Prerequisites

- Go 1.27+
- PostgreSQL 16 (or `docker compose up postgres`)
- protoc + `protoc-gen-go` + `protoc-gen-go-grpc` (only to regenerate protos)
- For real agents: libvirt (`qemu:///system`), `iptables`/`ip6tables`,
  `qemu-img`, and a configured bridge (e.g. `br-public`)

## Quick start (development)

```bash
# 1. Start PostgreSQL
docker compose up -d postgres

# 2. Configure environment
cp .env.example .env

# 3. Run the Control Plane (applies migrations on startup)
go run ./cmd/control-plane

# 4. Run an agent WITHOUT a hypervisor (fake mode) — or on a real host
AGENT_FAKE_MODE=true AGENT_ID=agent-1 go run ./cmd/agent
```

The API is now on `http://localhost:8080`, the agent gRPC on `:9001`.

### Register a node + provision a VM (curl walkthrough)

```bash
API=http://localhost:8080

# register a user
curl -s -X POST $API/v1/auth/register -H 'Content-Type: application/json' -d '{
  "email":"taro@example.com","password":"supersecret1",
  "first_name":"Taro","last_name":"Yamada","legal_name":"Taro Yamada",
  "country":"JP","postal_code":"100-0001","state":"Tokyo","city":"Chiyoda",
  "address_line_1":"1-1 Marunouchi"}'

TOKEN=$(curl -s -X POST $API/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"taro@example.com","password":"supersecret1"}' | jq -r .data.token)

# create a cluster and register the fake agent (requires support/admin role)
curl -s -X POST $API/v1/clusters -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"dc1"}'
# promote the user to admin for this walkthrough:
#   UPDATE users SET ... ; INSERT INTO user_roles ...  (or register a second user as admin)
curl -s -X POST $API/v1/nodes -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"cluster_id":"<cluster-id>","name":"agent-1","agent_endpoint":"localhost:9001"}'

# create a plan, network (with bridge) and image
curl -s -X POST $API/v1/plans -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"starter","vcpu":2,"memory_mb":2048,"disk_gb":40,"monthly_price_cents":1500}'
curl -s -X POST $API/v1/networks -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"public","bridge":"br-public"}'
curl -s -X POST $API/v1/networks/<net-id>/pools -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"cidr":"203.0.113.0/24","type":"ipv4","gateway":"203.0.113.1"}'

# request a VM (async)
curl -s -X POST $API/v1/vms -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: create-web-1" \
  -d '{"plan_id":"<plan-id>","network_id":"<net-id>","image_id":"<image-id>","hostname":"web-1"}'

# poll the operation
curl -s $API/v1/operations/<operation-id> -H "Authorization: Bearer $TOKEN"
```

## Database

Migrations live in [`migrations/`](migrations) and are applied automatically
on Control Plane startup (embedded, advisory-locked, transactional).

| Table | Purpose |
|-------|---------|
| users, roles, user_roles | accounts + RBAC |
| user_profiles | PII (legal name / address), isolated |
| sessions | opaque session tokens (hashed) |
| login_attempts | rate limiting / lockout |
| clusters, nodes, storage_pools | cluster metadata + heartbeats |
| plans, plan_versions | plans + append-only history |
| networks, ip_pools, ip_allocations | networks + IPAM |
| images | base OS images |
| vms | VM records (MAC unique) |
| vm_operations | async operations (idempotency keys) |
| billing_customers, subscriptions, invoices, payments | Stripe mirror |
| webhook_events | Stripe webhook idempotency |
| console_tokens | one-time console access |
| audit_logs | security events |

## Configuration

Configuration is via environment variables (see [`.env.example`](.env.example)):

```text
DATABASE_URL
STRIPE_SECRET_KEY / STRIPE_WEBHOOK_SECRET
HTTP_LISTEN_ADDRESS / PUBLIC_BASE_URL
GRPC_LISTEN_ADDRESS / GRPC_CLIENT_TIMEOUT
LIBVIRT_URI / AGENT_STORAGE_POOL / AGENT_WORK_DIR
TLS_CERT_FILE / TLS_KEY_FILE / TLS_CA_FILE   (mTLS, Control Plane side)
AGENT_TLS_*                                 (mTLS, Agent side)
```

Generate dev mTLS certificates with `make certs` and enable with
`TLS_ENABLED=true` + `AGENT_TLS_ENABLED=true`.

## Stripe setup

1. Create a Stripe account and a Price for each plan.
2. Set `STRIPE_SECRET_KEY`.
3. Create a webhook endpoint → `POST {PUBLIC_BASE_URL}/v1/webhooks/stripe`
   for `invoice.payment_succeeded`, `invoice.payment_failed`,
   `customer.subscription.created|updated|deleted`, `payment_intent.succeeded`.
4. Set `STRIPE_WEBHOOK_SECRET`.

Webhooks are signature-verified and processed idempotently.

## libvirt setup (real agents)

```bash
# on the hypervisor host
sudo systemctl enable --now libvirtd
sudo virsh pool-define-as default dir --target /var/lib/libvirt/images
sudo virsh pool-start default && sudo virsh pool-autostart default
sudo ip link add br-public type bridge && sudo ip link set br-public up
```

Run the agent on that host (not in Docker) with `LIBVIRT_URI=qemu:///system`.

## Testing

```bash
make unit           # unit tests (no DB)
make integration    # integration tests (needs PostgreSQL; TEST_DATABASE_URL)
make e2e            # full provisioning flow (real gRPC + fake libvirt)
make vet
make lint           # golangci-lint
```

E2E (`tests/e2e`) walks the whole lifecycle: registration → login → session →
plan/network/image → VM create → scheduler → gRPC agent → IP/MAC allocation →
cloud-init ISO → domain define/start → stop → start → delete → IP release →
MAC/iattr cleanup → idempotency.

## CI

`.github/workflows/ci.yml` runs `gofmt`, `go vet`, `go build`, unit +
integration + e2e tests against a PostgreSQL service container, and
`golangci-lint`.

## Reproducible protos

```bash
make proto   # regenerate proto/gen from proto/
```

## License

Private/internal project. See repository owner for licensing.