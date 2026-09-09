package vms

import (
	"context"
	"errors"
	"testing"

	"github.com/tuna2134/vps/internal/controlplane/models"
)

// billingTestStore implements VMStore.
type billingTestStore struct {
	vm  *models.VM
	ops map[string]*models.VMOperation
}

func (b *billingTestStore) UpdateStatus(ctx context.Context, id string, status models.VMStatus) error {
	if b.vm != nil && b.vm.ID == id {
		b.vm.Status = status
	}
	return nil
}
func (b *billingTestStore) SoftDelete(ctx context.Context, id string) error {
	if b.vm != nil && b.vm.ID == id {
		b.vm.Status = models.VMStatusTerminated
	}
	return nil
}
func (b *billingTestStore) GetByID(ctx context.Context, id string) (*models.VM, error) {
	if b.vm == nil || b.vm.ID != id {
		return nil, ErrNotFound
	}
	return b.vm, nil
}
func (b *billingTestStore) ListByUser(ctx context.Context, userID string) ([]models.VM, error) {
	if b.vm != nil && b.vm.UserID == userID {
		return []models.VM{*b.vm}, nil
	}
	return nil, nil
}
func (b *billingTestStore) Create(ctx context.Context, vm *models.VM) (*models.VM, error) {
	b.vm = vm
	return vm, nil
}
func (b *billingTestStore) Exists(ctx context.Context, mac string) (bool, error) { return false, nil }

// opTestStore implements OperationStore.
type opTestStore struct {
	ops map[string]*models.VMOperation
}

func (o *opTestStore) GetByID(ctx context.Context, id string) (*models.VMOperation, error) {
	op, ok := o.ops[id]
	if !ok {
		return nil, ErrNotFound
	}
	return op, nil
}
func (o *opTestStore) GetByIdempotencyKey(ctx context.Context, key string) (*models.VMOperation, error) {
	for _, op := range o.ops {
		if op.IdempotencyKey == key {
			return op, nil
		}
	}
	return nil, ErrNotFound
}
func (o *opTestStore) Create(ctx context.Context, op *models.VMOperation) (*models.VMOperation, error) {
	op.ID = "op-" + op.IdempotencyKey
	o.ops[op.ID] = op
	return op, nil
}
func (o *opTestStore) MarkRunning(ctx context.Context, id string) error   { return nil }
func (o *opTestStore) MarkSucceeded(ctx context.Context, id string) error { return nil }
func (o *opTestStore) MarkFailed(ctx context.Context, id, e string) error { return nil }

func newBillingTestService(active bool) *Service {
	vmStore := &billingTestStore{
		vm:  &models.VM{ID: "vm-1", UserID: "u-1", Status: models.VMStatusStopped},
		ops: map[string]*models.VMOperation{},
	}
	opStore := &opTestStore{ops: vmStore.ops}
	gate := func(ctx context.Context, userID string) (bool, error) { return active, nil }
	return &Service{
		provisioner: &Provisioner{jobs: make(chan ProvisionJob, 8), stop: make(chan struct{})},
		billingGate: gate,
		vms:         vmStore,
		ops:         opStore,
	}
}

var _ VMStore = (*billingTestStore)(nil)
var _ OperationStore = (*opTestStore)(nil)

// TestStartBlockedWhenBillingInactive verifies a suspended/unpaid user cannot
// start or reboot their VMs (billing bypass fix).
func TestStartBlockedWhenBillingInactive(t *testing.T) {
	ctx := context.Background()
	svc := newBillingTestService(false) // billing inactive

	if _, err := svc.NewOperation(ctx, "u-1", "vm-1", models.OperationStart, "k1"); !errors.Is(err, ErrBillingRequired) {
		t.Errorf("start must require active billing, got %v", err)
	}
	if _, err := svc.NewOperation(ctx, "u-1", "vm-1", models.OperationReboot, "k2"); !errors.Is(err, ErrBillingRequired) {
		t.Errorf("reboot must require active billing, got %v", err)
	}
	// Stop/delete remain allowed for a suspended user (they reduce compute).
	if _, err := svc.NewOperation(ctx, "u-1", "vm-1", models.OperationDelete, "k3"); err != nil {
		t.Errorf("delete should be allowed for suspended user, got %v", err)
	}
}

// TestStartAllowedWhenBillingActive verifies an active user can start VMs.
func TestStartAllowedWhenBillingActive(t *testing.T) {
	ctx := context.Background()
	svc := newBillingTestService(true)
	if _, err := svc.NewOperation(ctx, "u-1", "vm-1", models.OperationStart, "k4"); err != nil {
		t.Errorf("start should be allowed with active billing, got %v", err)
	}
}

// TestCreateBlockedWhenBillingInactive verifies VM creation fails closed.
func TestCreateBlockedWhenBillingInactive(t *testing.T) {
	ctx := context.Background()
	svc := newBillingTestService(false)
	if _, _, err := svc.CreateVM(ctx, "u-1", CreateRequest{}, "k5"); !errors.Is(err, ErrBillingRequired) {
		t.Errorf("create must require active billing, got %v", err)
	}
}

// TestCreateBlockedWhenNoGate verifies fail-closed behavior (no billing gate).
func TestCreateBlockedWhenNoGate(t *testing.T) {
	ctx := context.Background()
	svc := &Service{
		provisioner: &Provisioner{jobs: make(chan ProvisionJob, 8), stop: make(chan struct{})},
		vms:         &billingTestStore{vm: &models.VM{ID: "vm-1", UserID: "u-1", Status: models.VMStatusStopped}},
		ops:         &opTestStore{ops: map[string]*models.VMOperation{}},
	}
	if _, _, err := svc.CreateVM(ctx, "u-1", CreateRequest{}, "k6"); !errors.Is(err, ErrBillingRequired) {
		t.Errorf("create must fail closed without a billing gate, got %v", err)
	}
}

// TestIdempotencyKeyScopedPerUser verifies one user's idempotency key can
// never resolve another user's operation (cross-user leak fix).
func TestIdempotencyKeyScopedPerUser(t *testing.T) {
	ctx := context.Background()

	// User A's operation exists for the shared key.
	seedOps := map[string]*models.VMOperation{}
	opA, err := (&opTestStore{ops: seedOps}).Create(ctx, &models.VMOperation{
		VMID:           "vm-1",
		OperationType:  models.OperationStart,
		IdempotencyKey: idempotencyKeyFor("u-1", "shared-key"),
	})
	if err != nil {
		t.Fatalf("seed op: %v", err)
	}

	// The same user retrying gets their own op back (idempotency preserved).
	svcA := &Service{
		provisioner: &Provisioner{jobs: make(chan ProvisionJob, 8), stop: make(chan struct{})},
		billingGate: func(ctx context.Context, userID string) (bool, error) { return true, nil },
		vms:         &billingTestStore{vm: &models.VM{ID: "vm-1", UserID: "u-1", Status: models.VMStatusStopped}},
		ops:         &opTestStore{ops: seedOps},
	}
	gotA, err := svcA.NewOperation(ctx, "u-1", "vm-1", models.OperationStart, "shared-key")
	if err != nil {
		t.Fatalf("user A retry failed: %v", err)
	}
	if gotA.ID != opA.ID {
		t.Fatalf("user A retry must return the original op, got %q want %q", gotA.ID, opA.ID)
	}

	// A different user replaying the key must NOT resolve user A's op.
	svcB := &Service{
		provisioner: &Provisioner{jobs: make(chan ProvisionJob, 8), stop: make(chan struct{})},
		billingGate: func(ctx context.Context, userID string) (bool, error) { return true, nil },
		vms:         &billingTestStore{vm: &models.VM{ID: "vm-1", UserID: "u-1", Status: models.VMStatusStopped}},
		ops:         &opTestStore{ops: seedOps},
	}
	gotB, err := svcB.NewOperation(ctx, "u-2", "vm-1", models.OperationStart, "shared-key")
	if err == nil {
		t.Fatalf("user B must not see user A's op, got %q", gotB.ID)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("user B should hit ownership check (ErrNotFound), got %v", err)
	}
}
