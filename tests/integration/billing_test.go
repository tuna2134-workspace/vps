package integration

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/webhook"

	"github.com/tuna2134/vps/internal/controlplane/audit"
	"github.com/tuna2134/vps/internal/controlplane/billing"
)

// TestStripeWebhookIdempotency verifies that processing the same webhook
// twice has no side effects (no duplicate rows / no double processing).
func TestStripeWebhookIdempotency(t *testing.T) {
	ctx := newContext(t)
	secret := "whsec_testsecret"
	svc := billing.NewService("sk_test_fake", secret, repos.Billing, audit.NewService(repos.Audit), slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Create a local billing customer so the handler can resolve the user.
	user := newTestUser(t, ctx)
	customerID := "cus_" + randSuffix()
	if _, err := repos.Billing.UpsertCustomer(ctx, user.ID, customerID); err != nil {
		t.Fatalf("create billing customer: %v", err)
	}

	processed := 0
	svc.OnPaymentFailed = func(c context.Context, userID string) error {
		processed++
		return nil
	}

	eventID := "evt_idem_" + randSuffix()
	payload := []byte(`{
		"id": "` + eventID + `",
		"object": "event",
		"type": "invoice.payment_failed",
		"data": {"object": {"id": "in_test", "object": "invoice", "customer": "` + customerID + `"}}
	}`)

	// Build a signed payload so ConstructEvent succeeds.
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload:   payload,
		Secret:    secret,
		Timestamp: time.Now(),
		Scheme:    "v1",
	})

	// First processing: should be handled.
	handled, err := svc.HandleWebhook(ctx, signed.Payload, signed.Header)
	if err != nil {
		t.Fatalf("first HandleWebhook: %v", err)
	}
	if !handled {
		t.Fatal("expected first event to be processed")
	}
	if processed != 1 {
		t.Errorf("expected 1 callback invocation, got %d", processed)
	}

	// Second processing of the same event id: must be a no-op.
	handled, err = svc.HandleWebhook(ctx, signed.Payload, signed.Header)
	if err != nil {
		t.Fatalf("second HandleWebhook: %v", err)
	}
	if handled {
		t.Error("duplicate event must be acknowledged as already processed")
	}
	if processed != 1 {
		t.Errorf("callback must not run twice, got %d invocations", processed)
	}
}

// TestStripeWebhookBadSignature verifies that unsigned/mismatched payloads are
// rejected.
func TestStripeWebhookBadSignature(t *testing.T) {
	ctx := newContext(t)
	svc := billing.NewService("sk_test_fake", "whsec_secret", repos.Billing, audit.NewService(repos.Audit), slog.New(slog.NewTextHandler(io.Discard, nil)))
	payload := []byte(`{"id":"evt_x","object":"event","type":"invoice.payment_failed","data":{"object":{"id":"in_x"}}}`)
	if _, err := svc.HandleWebhook(ctx, payload, "t=1,v1=forged"); err == nil {
		t.Fatal("expected signature verification to fail")
	}
}

var _ = stripe.PaymentIntent{}
