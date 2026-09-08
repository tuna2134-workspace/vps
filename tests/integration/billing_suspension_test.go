package integration

import (
	"testing"
	"time"

	"github.com/tuna2134/vps/internal/controlplane/models"
)

// TestBillingSuspensionLifecycle verifies the overdue-payment flow: mark
// suspended (with a termination deadline), list due-for-termination after the
// grace period, and reactivate clears the suspension.
func TestBillingSuspensionLifecycle(t *testing.T) {
	ctx := newContext(t)

	user := newTestUser(t, ctx)
	stripeSubID := "sub_suspend_" + randSuffix()
	if _, err := repos.Billing.UpsertSubscription(ctx, &models.Subscription{
		UserID:               user.ID,
		StripeSubscriptionID: stripeSubID,
		Status:               "active",
		BillingStatus:        "active",
	}); err != nil {
		t.Fatalf("upsert subscription: %v", err)
	}

	// Payment fails: suspend and schedule termination in 7 days.
	terminateAt := time.Now().Add(7 * 24 * time.Hour)
	if err := repos.Billing.SetSubscriptionSuspended(ctx, stripeSubID, terminateAt); err != nil {
		t.Fatalf("set suspended: %v", err)
	}
	sub, err := repos.Billing.GetSubscriptionByStripeID(ctx, stripeSubID)
	if err != nil {
		t.Fatalf("get subscription: %v", err)
	}
	if sub.BillingStatus != "suspended" || sub.TerminateAt == nil {
		t.Errorf("expected suspended with terminate_at: %+v", sub)
	}
	if sub.SuspendedAt == nil {
		t.Errorf("suspended_at not set")
	}

	// Not yet due.
	due, err := repos.Billing.ListSuspendedDueForTermination(ctx, time.Now().Add(3*24*time.Hour))
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("subscription must not be due before grace period, got %d", len(due))
	}

	// After the grace period, it becomes due.
	due, err = repos.Billing.ListSuspendedDueForTermination(ctx, terminateAt.Add(time.Second))
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 1 || due[0].StripeSubscriptionID != stripeSubID {
		t.Errorf("expected subscription due for termination: %+v", due)
	}

	// Payment recovers: clearing the suspension removes it from the sweep.
	if err := repos.Billing.SetSubscriptionActive(ctx, stripeSubID); err != nil {
		t.Fatalf("set active: %v", err)
	}
	sub, _ = repos.Billing.GetSubscriptionByStripeID(ctx, stripeSubID)
	if sub.BillingStatus != "active" || sub.TerminateAt != nil || sub.SuspendedAt != nil {
		t.Errorf("expected active with no suspension: %+v", sub)
	}
	due, _ = repos.Billing.ListSuspendedDueForTermination(ctx, time.Now().Add(30*24*time.Hour))
	if len(due) != 0 {
		t.Errorf("subscription must not be due after reactivation, got %d", len(due))
	}
}

// TestTerminateOperationPersists verifies the terminate operation type is
// accepted by the operation repository and lifecycle.
func TestTerminateOperationPersists(t *testing.T) {
	ctx := newContext(t)
	user := newTestUser(t, ctx)
	vm := newTestVM(t, ctx, user.ID)

	op, err := repos.Operations.Create(ctx, &models.VMOperation{
		VMID:           vm.ID,
		OperationType:  models.OperationTerminate,
		IdempotencyKey: "term-" + randSuffix(),
	})
	if err != nil {
		t.Fatalf("create terminate operation: %v", err)
	}
	if op.OperationType != models.OperationTerminate {
		t.Errorf("operation type wrong: %s", op.OperationType)
	}
	// A terminate operation can be marked succeeded like any other.
	if err := repos.Operations.MarkRunning(ctx, op.ID); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	if err := repos.Operations.MarkSucceeded(ctx, op.ID); err != nil {
		t.Fatalf("mark succeeded: %v", err)
	}
}
