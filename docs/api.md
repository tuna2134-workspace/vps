# API

The full OpenAPI 3.x contract is in [`api/openapi/openapi.yaml`](../api/openapi/openapi.yaml).
This document summarizes the conventions.

## Base URL

```
http://localhost:8080
```

## Envelope

All responses use a consistent envelope.

Success:

```json
{
  "data": {}
}
```

Error:

```json
{
  "error": {
    "code": "RESOURCE_NOT_FOUND",
    "message": "resource not found"
  }
}
```

Internal error details and stack traces are never returned to clients.

## Authentication

Log in to obtain an opaque bearer token:

```
POST /v1/auth/login
{"email": "taro@example.com", "password": "supersecret1"}
```

Response:

```json
{
  "data": {
    "token": "6mxd5m8JqfjR9V...",
    "expires_at": "2026-09-09T04:00:00Z"
  }
}
```

Send it on every authenticated request:

```
Authorization: Bearer 6mxd5m8JqfjR9V...
```

Roles (`user`, `support`, `admin`) gate admin endpoints. Permission checks live
in middleware, not in handlers.

## Endpoint summary

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/healthz` | – | Liveness |
| GET | `/readyz` | – | Readiness |
| POST | `/v1/auth/register` | – | Register account (incl. legal name + address) |
| POST | `/v1/auth/login` | – | Login, returns session token |
| POST | `/v1/auth/logout` | ✓ | Revoke current session |
| GET | `/v1/auth/sessions` | ✓ | List sessions |
| DELETE | `/v1/auth/sessions/{id}` | ✓ | Revoke a session |
| POST | `/v1/auth/sessions/revoke_all` | ✓ | Revoke all other sessions |
| POST | `/v1/auth/change-password` | ✓ | Change password (revokes others) |
| GET | `/v1/users/me` | ✓ | Current user |
| GET | `/v1/plans` | – | List plans |
| GET | `/v1/plans/{id}` | – | Get plan |
| POST | `/v1/plans` | support | Create plan |
| PUT | `/v1/plans/{id}` | support | Update plan (versions snapshot) |
| GET | `/v1/plans/{id}/versions` | support | Plan history |
| PATCH | `/v1/plans/{id}/active` | support | Enable/disable plan |
| GET | `/v1/networks` | ✓ | List networks |
| POST | `/v1/networks` | support | Create network (bridge required) |
| GET | `/v1/networks/{id}` | ✓ | Get network |
| GET | `/v1/networks/{id}/pools` | ✓ | List IP pools |
| POST | `/v1/networks/{id}/pools` | support | Add IP pool |
| PATCH | `/v1/networks/{id}/status` | support | Set active/inactive |
| GET | `/v1/clusters` | support | List clusters |
| POST | `/v1/clusters` | support | Create cluster |
| GET | `/v1/nodes` | support | List nodes |
| POST | `/v1/nodes` | support | Register node |
| GET | `/v1/nodes/{id}` | support | Get node |
| GET | `/v1/vms` | ✓ | List my VMs |
| POST | `/v1/vms` | ✓ | Request VM (async) |
| GET | `/v1/vms/{id}` | ✓ | Get VM |
| DELETE | `/v1/vms/{id}` | ✓ | Delete VM (async) |
| POST | `/v1/vms/{id}/start` | ✓ | Start VM (async) |
| POST | `/v1/vms/{id}/stop` | ✓ | Stop VM (async) |
| POST | `/v1/vms/{id}/force_stop` | ✓ | Force-stop VM (async) |
| POST | `/v1/vms/{id}/reboot` | ✓ | Reboot VM (async) |
| POST | `/v1/vms/{id}/console` | ✓ | One-time console token |
| GET | `/console/ws` | token | noVNC WebSocket gateway |
| GET | `/v1/operations/{id}` | ✓ | Poll operation |
| GET | `/v1/billing/subscription` | ✓ | My subscription |
| POST | `/v1/billing/subscription` | ✓ | Create subscription |
| GET | `/v1/billing/invoices` | ✓ | My invoices |
| POST | `/v1/webhooks/stripe` | – | Stripe webhook (signature verified) |

## Asynchronous operations

VM lifecycle changes return immediately with an operation:

```json
{
  "data": {
    "operation_id": "4f6d1a2b-...",
    "vm_id": "3e7d9c5c-..."
  }
}
```

Poll the operation state:

```
GET /v1/operations/{id}
```

```json
{
  "data": {
    "id": "4f6d1a2b-...",
    "vm_id": "3e7d9c5c-...",
    "operation_type": "create",
    "status": "succeeded",
    "created_at": "2026-09-08T03:00:00Z",
    "updated_at": "2026-09-08T03:01:00Z"
  }
}
```

States: `pending`, `running`, `succeeded`, `failed`.

### Idempotency

Send an `Idempotency-Key` header (or an `idempotency_key` field) when creating
a VM. Replaying the same key returns the original operation and never creates a
second VM.

## Console (noVNC / serial)

1. `POST /v1/vms/{id}/console` returns a one-time token + websocket URL. Set
   `console_type` to `vnc` (graphical, noVNC) or `serial` (text console).
2. The client connects to the websocket URL.
3. The gateway validates the token and bridges the socket to the VM's console
   endpoint — TCP for VNC, a Unix domain socket for the serial PTY. Neither is
   ever exposed to the client.

```json
{
  "data": {
    "console_type": "serial",
    "token": "5xt9...",
    "expires_at": "2026-09-08T03:02:00Z",
    "websocket_url": "wss://panel.example.com/console/ws?token=5xt9..."
  }
}
```

## Error codes

| Code | HTTP | Meaning |
|------|------|---------|
| `INVALID_REQUEST` | 400 | Malformed body |
| `VALIDATION_ERROR` | 400 | Field validation failed |
| `INVALID_CREDENTIALS` | 401 | Bad email/password (generic) |
| `UNAUTHENTICATED` | 401 | Missing/invalid token |
| `SESSION_EXPIRED` | 401 | Token expired |
| `SESSION_REVOKED` | 401 | Token revoked |
| `EMAIL_TAKEN` | 409 | Duplicate email |
| `CONFLICT` | 409 | Resource exists |
| `INVALID_STATE` | 409 | VM not in valid state |
| `FORBIDDEN` | 403 | Role insufficient |
| `ACCOUNT_DISABLED` | 403 | Account disabled |
| `RESOURCE_NOT_FOUND` | 404 | Not found |
| `BILLING_REQUIRED` | 402 | No active subscription |
| `TOO_MANY_ATTEMPTS` | 429 | Login rate limited |
| `NO_CAPACITY` | 503 | No node capacity |
| `INTERNAL_ERROR` | 500 | Unexpected (no detail leaked) |