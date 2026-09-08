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
Payment successful       -> provision VPS (billing gate opens)
Payment failed           -> mark billing_state past_due -> grace -> suspend VPS
Subscription canceled    -> terminate or schedule termination
```

The webhook handler is synchronous and fast; long-running side effects (VM
provisioning, suspension) are delegated to the operation worker via callbacks,
so Stripe never waits on virtualization work.

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
STRIPE_SECRET_KEY      sk_test_...
STRIPE_WEBHOOK_SECRET  whsec_...
```

Never commit secrets. See `.env.example`.