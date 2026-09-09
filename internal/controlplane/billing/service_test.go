package billing

import (
	"testing"

	"github.com/stripe/stripe-go/v81"
)

// TestBillingStatusFor verifies that only active/trialing subscriptions enable
// VM compute. Every other Stripe state (incomplete, incomplete_expired, paused,
// unpaid, past_due, canceled) must NOT report "active" — otherwise an unpaid
// user could create VMs for free.
func TestBillingStatusFor(t *testing.T) {
	active := []stripe.SubscriptionStatus{
		stripe.SubscriptionStatusActive,
		stripe.SubscriptionStatusTrialing,
	}
	for _, st := range active {
		if got := billingStatusFor(st); got != "active" {
			t.Errorf("billingStatusFor(%s) = %q, want active", st, got)
		}
	}

	blocked := []stripe.SubscriptionStatus{
		stripe.SubscriptionStatusIncomplete,
		stripe.SubscriptionStatusIncompleteExpired,
		stripe.SubscriptionStatusPaused,
		stripe.SubscriptionStatusUnpaid,
		stripe.SubscriptionStatusPastDue,
		stripe.SubscriptionStatusCanceled,
		"mystery_status",
	}
	for _, st := range blocked {
		if got := billingStatusFor(st); got == "active" {
			t.Errorf("billingStatusFor(%s) = %q, must NOT be active", st, got)
		}
	}
}
