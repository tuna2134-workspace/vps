# Billing

Billing uses Stripe (`stripe-go/v81`).

## Entities

- **Customer** — one per user (`billing_customers` maps `user_id` →
  `stripe_customer_id`).
- **Subscription** — created from a Stripe Price; drives the billing gate for
  VM provisioning.
- **Invoice** — mirrored from Stripe on `invoice.payment_succeeded`.
- **Payment** — mirrored from `payment_intent.succeeded`.
- **Webhook events** — `webhook_events` stores `stripe_event_id` (UNIQUE) so
  every event is processed at most once.

## Lifecycle integration

```text
Payment successful       -> subscription active (billing gate opens)
Payment failed           -> subscription suspended; VMs shut down immediately
                           -> grace period (default 7 days)
                           -> debt settled: subscription reactivated (VMs stay off)
                           -> grace period expires: VMs terminated automatically
Subscription canceled    -> all of the user's VMs terminated
```

The webhook handler is synchronous and fast; long-running side effects (VM
suspension, termination) are delegated to the operation worker via callbacks,
so Stripe never waits on virtualization work.

A background sweeper (`billingSweeper`) runs every `BILLING_SWEEP_INTERVAL`
(default 1h) and terminates the VMs of any subscription that is still
`suspended` after `BILLING_GRACE_PERIOD` (default `168h` = 7 days). The
subscription records `suspended_at` / `terminate_at` to drive this.

## Webhook security

- Every webhook is verified with `webhook.ConstructEventWithOptions` using the
  configured `STRIPE_WEBHOOK_SECRET`. Requests with a bad signature are
  rejected with 400.
- Processing is idempotent: the event ID is inserted with `ON CONFLICT DO
  NOTHING`; if the insert affected zero rows the event was already processed
  and is acknowledged without side effects. Duplicate delivery can never
  double-charge or double-provision. (Covered by
  `TestStripeWebhookIdempotency`.)

## Operation / billing gates

`POST /v1/vms` consults the user's subscription (`billing_status = 'active'`)
before creating a VM. The gate is injected into the VM service as an interface,
so billing can be extended (trial, credits) without touching provisioning.

## Configuration

```text
STRIPE_SECRET_KEY       sk_test_...
STRIPE_WEBHOOK_SECRET   whsec_...
BILLING_GRACE_PERIOD    168h    # overdue grace period before VM termination
BILLING_SWEEP_INTERVAL  1h      # how often the termination sweeper runs
```

Never commit secrets. See `.env.example`.