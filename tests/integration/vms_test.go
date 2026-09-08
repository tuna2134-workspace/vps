package integration

import (
	"errors"
	"testing"

	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/repositories"
)

// TestVMPersistence verifies VM creation, lookup, and operation lifecycle.
func TestVMPersistence(t *testing.T) {
	ctx := newContext(t)
	user := newTestUser(t, ctx)
	mac := uniqueMAC()

	vm, err := repos.VMs.Create(ctx, &models.VM{
		UserID:     user.ID,
		Name:       "web-1",
		Hostname:   "web-1",
		Status:     models.VMStatusPending,
		VCPU:       2,
		MemoryMB:   2048,
		DiskGB:     40,
		MACAddress: mac,
		InstanceID: "i-" + randSuffix(),
	})
	if err != nil {
		t.Fatalf("create vm: %v", err)
	}

	got, err := repos.VMs.GetByID(ctx, vm.ID)
	if err != nil {
		t.Fatalf("get vm: %v", err)
	}
	if got.Status != models.VMStatusPending || got.VCPU != 2 {
		t.Errorf("vm fields wrong: %+v", got)
	}

	// Unique MAC constraint.
	if _, err := repos.VMs.Create(ctx, &models.VM{
		UserID:     user.ID,
		Name:       "dup",
		Hostname:   "dup",
		Status:     models.VMStatusPending,
		VCPU:       1,
		MemoryMB:   1024,
		DiskGB:     10,
		MACAddress: mac,
		InstanceID: "i-dup",
	}); err == nil {
		t.Error("expected duplicate MAC to fail")
	}

	// Operation lifecycle.
	op, err := repos.Operations.Create(ctx, &models.VMOperation{
		VMID:           vm.ID,
		OperationType:  models.OperationStart,
		IdempotencyKey: "key-" + randSuffix(),
	})
	if err != nil {
		t.Fatalf("create op: %v", err)
	}
	if err := repos.Operations.MarkRunning(ctx, op.ID); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	if err := repos.Operations.MarkSucceeded(ctx, op.ID); err != nil {
		t.Fatalf("mark succeeded: %v", err)
	}
	final, err := repos.Operations.GetByID(ctx, op.ID)
	if err != nil {
		t.Fatalf("get op: %v", err)
	}
	if final.Status != models.OperationSucceeded || final.FinishedAt == nil {
		t.Errorf("operation not terminal: %+v", final)
	}
}

// TestOperationIdempotency verifies the unique idempotency key constraint.
func TestOperationIdempotency(t *testing.T) {
	ctx := newContext(t)
	key := "idem-" + randSuffix()
	_, err := repos.Operations.Create(ctx, &models.VMOperation{OperationType: models.OperationCreate, IdempotencyKey: key})
	if err != nil {
		t.Fatalf("create op 1: %v", err)
	}
	if _, err := repos.Operations.Create(ctx, &models.VMOperation{OperationType: models.OperationCreate, IdempotencyKey: key}); !errors.Is(err, repositories.ErrConflict) {
		t.Errorf("expected ErrConflict for duplicate idempotency key, got %v", err)
	}
}
