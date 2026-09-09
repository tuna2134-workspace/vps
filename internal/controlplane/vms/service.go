// Package vms implements VM lifecycle management and asynchronous operations.
// VM creation is intentionally asynchronous: the API returns an operation ID
// and a worker drives the multi-step provisioning flow against the selected
// agent.
package vms

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/tuna2134/vps/internal/controlplane/audit"
	"github.com/tuna2134/vps/internal/controlplane/macalloc"
	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/networks"
	"github.com/tuna2134/vps/internal/controlplane/repositories"
	"github.com/tuna2134/vps/internal/controlplane/scheduler"
)

var (
	ErrNotFound         = errors.New("not found")
	ErrConflict         = errors.New("conflict")
	ErrNoCapacity       = errors.New("no capacity")
	ErrBillingRequired  = errors.New("active billing subscription required")
	ErrInvalidState     = errors.New("invalid state for operation")
	ErrAgentUnavailable = errors.New("agent unavailable")
)

// BillingGate is implemented by the billing layer to enforce that a user has
// an active subscription before provisioning.
type BillingGate func(ctx context.Context, userID string) (bool, error)

type Service struct {
	vms         VMStore
	ops         OperationStore
	nodes       *repositories.NodeRepository
	plans       *repositories.PlanRepository
	networks    *networks.Service
	scheduler   *scheduler.Scheduler
	mac         *macalloc.Generator
	audit       *audit.Service
	provisioner *Provisioner
	billingGate BillingGate
}

func NewService(
	vms VMStore,
	ops OperationStore,
	nodes *repositories.NodeRepository,
	plans *repositories.PlanRepository,
	networkSvc *networks.Service,
	sched *scheduler.Scheduler,
	macGen *macalloc.Generator,
	auditSvc *audit.Service,
	provisioner *Provisioner,
	billingGate BillingGate,
) *Service {
	return &Service{
		vms:         vms,
		ops:         ops,
		nodes:       nodes,
		plans:       plans,
		networks:    networkSvc,
		scheduler:   sched,
		mac:         macGen,
		audit:       auditSvc,
		provisioner: provisioner,
		billingGate: billingGate,
	}
}

// CreateRequest describes a new VM request.
type CreateRequest struct {
	PlanID    string
	NetworkID string
	ImageID   string
	Name      string
	Hostname  string
	VCPU      int
	MemoryMB  int
	DiskGB    int
	// SSHKeys are passed through to cloud-init.
	SSHKeys []string
	// RootPassword optionally seeds a cloud-init password.
	RootPassword string
}

// CreateVM registers a VM and enqueues the provisioning operation. It returns
// the operation that drives provisioning. The idempotency key guarantees the
// request can be safely retried. Resources are derived from the plan when not
// explicitly set.
func (s *Service) CreateVM(ctx context.Context, userID string, req CreateRequest, idempotencyKey string) (*models.VM, *models.VMOperation, error) {
	if idempotencyKey == "" {
		idempotencyKey = uuid.NewString()
	}
	// Scope the key to the user so one user's key can never resolve another
	// user's operation (cross-user information leak).
	key := idempotencyKeyFor(userID, idempotencyKey)

	// Idempotency: if an operation with this key already exists, return it.
	if op, err := s.ops.GetByIdempotencyKey(ctx, key); err == nil {
		vm, err := s.vms.GetByID(ctx, op.VMID)
		if err != nil {
			return nil, op, err
		}
		return vm, op, nil
	}

	// Billing status check.
	if ok, err := s.billingAllowed(ctx, userID); err != nil {
		return nil, nil, err
	} else if !ok {
		return nil, nil, ErrBillingRequired
	}

	// Resolve resources from the plan when not provided explicitly.
	plan, err := s.plans.GetByID(ctx, req.PlanID)
	if err != nil {
		return nil, nil, ErrNotFound
	}
	if req.VCPU <= 0 {
		req.VCPU = plan.VCPU
	}
	if req.MemoryMB <= 0 {
		req.MemoryMB = plan.MemoryMB
	}
	if req.DiskGB <= 0 {
		req.DiskGB = plan.DiskGB
	}

	// Atomically select a node and reserve its capacity. The reservation is
	// released if any later step fails and committed once provisioning
	// succeeds.
	vmID := uuid.NewString()
	node, err := s.scheduler.Reserve(ctx, vmID, scheduler.Request{
		VCPU:     req.VCPU,
		MemoryMB: req.MemoryMB,
		DiskGB:   req.DiskGB,
	})
	if err != nil {
		if errors.Is(err, scheduler.ErrNoCapacity) {
			return nil, nil, ErrNoCapacity
		}
		return nil, nil, err
	}
	releaseReservation := func() { _ = s.scheduler.Release(context.Background(), vmID) }

	// Allocate a unique MAC before persisting the VM so the unique index is
	// never violated.
	mac, err := s.mac.Generate(ctx, nil)
	if err != nil {
		releaseReservation()
		return nil, nil, fmt.Errorf("allocate mac: %w", err)
	}

	instanceID := uuid.NewString()
	vm, err := s.vms.Create(ctx, &models.VM{
		ID:           vmID,
		UserID:       userID,
		PlanID:       req.PlanID,
		NetworkID:    req.NetworkID,
		ImageID:      req.ImageID,
		NodeID:       node.ID,
		Name:         req.Name,
		Hostname:     req.Hostname,
		Status:       models.VMStatusPending,
		VCPU:         req.VCPU,
		MemoryMB:     req.MemoryMB,
		DiskGB:       req.DiskGB,
		MACAddress:   mac,
		InstanceID:   instanceID,
		SSHKeys:      req.SSHKeys,
		RootPassword: req.RootPassword,
	})
	if err != nil {
		releaseReservation()
		return nil, nil, err
	}

	// Allocate an IP address for the VM.
	alloc, err := s.networks.Allocate(ctx, req.NetworkID, vm.ID, mac)
	if err != nil {
		releaseReservation()
		_ = s.vms.SoftDelete(ctx, vm.ID)
		return nil, nil, err
	}

	op, err := s.ops.Create(ctx, &models.VMOperation{
		VMID:           vm.ID,
		OperationType:  models.OperationCreate,
		IdempotencyKey: key,
	})
	if err != nil {
		if errors.Is(err, repositories.ErrConflict) {
			// A concurrent request won the race; return the existing op.
			releaseReservation()
			existing, _ := s.ops.GetByIdempotencyKey(ctx, key)
			return vm, existing, nil
		}
		releaseReservation()
		_ = s.networks.Release(ctx, vm.ID)
		_ = s.vms.SoftDelete(ctx, vm.ID)
		return nil, nil, err
	}

	_ = s.audit.Record(ctx, audit.Event{
		UserID:       userID,
		Action:       "vm.create",
		ResourceType: "vm",
		ResourceID:   vm.ID,
		Metadata: map[string]any{
			"ip":   alloc.IPAddress,
			"node": node.ID,
		},
	})

	// Enqueue provisioning.
	s.provisioner.Enqueue(ProvisionJob{OperationID: op.ID, VMID: vm.ID, NodeID: node.ID})

	return vm, op, nil
}

func (s *Service) ListVMs(ctx context.Context, userID string) ([]models.VM, error) {
	return s.vms.ListByUser(ctx, userID)
}

func (s *Service) GetVM(ctx context.Context, userID, vmID string) (*models.VM, error) {
	vm, err := s.vms.GetByID(ctx, vmID)
	if err != nil {
		return nil, err
	}
	if vm.UserID != userID {
		return nil, ErrNotFound
	}
	return vm, nil
}

// NewOperation registers and enqueues a lifecycle operation for an existing VM.
func (s *Service) NewOperation(ctx context.Context, userID, vmID string, opType models.OperationType, idempotencyKey string) (*models.VMOperation, error) {
	if idempotencyKey == "" {
		idempotencyKey = uuid.NewString()
	}
	// Scope the key to the user so one user's key can never resolve another
	// user's operation (cross-user information leak).
	key := idempotencyKeyFor(userID, idempotencyKey)
	if op, err := s.ops.GetByIdempotencyKey(ctx, key); err == nil {
		return op, nil
	}
	vm, err := s.vms.GetByID(ctx, vmID)
	if err != nil {
		return nil, ErrNotFound
	}
	if vm.UserID != userID {
		return nil, ErrNotFound
	}

	// Operations that enable compute (start, reboot) require an active
	// subscription. Otherwise a suspended/unpaid user could restart a VM that
	// was shut down for non-payment, bypassing the suspension.
	if opType == models.OperationStart || opType == models.OperationReboot {
		if ok, err := s.billingAllowed(ctx, userID); err != nil {
			return nil, err
		} else if !ok {
			return nil, ErrBillingRequired
		}
	}

	if err := s.validateTransition(vm, opType); err != nil {
		return nil, err
	}

	op, err := s.ops.Create(ctx, &models.VMOperation{
		VMID:           vm.ID,
		OperationType:  opType,
		IdempotencyKey: key,
	})
	if err != nil {
		if errors.Is(err, repositories.ErrConflict) {
			return s.ops.GetByIdempotencyKey(ctx, key)
		}
		return nil, err
	}

	_ = s.audit.Record(ctx, audit.Event{
		UserID:       userID,
		Action:       "vm." + string(opType),
		ResourceType: "vm",
		ResourceID:   vm.ID,
	})
	s.provisioner.Enqueue(ProvisionJob{OperationID: op.ID, VMID: vm.ID, NodeID: vm.NodeID})
	return op, nil
}

// billingAllowed reports whether the user's billing is active. It fails
// closed: if no billing gate is configured, VM compute is denied.
func (s *Service) billingAllowed(ctx context.Context, userID string) (bool, error) {
	if s.billingGate == nil {
		return false, nil
	}
	return s.billingGate(ctx, userID)
}

// idempotencyKeyFor namespaces a client-supplied idempotency key to a single
// user. Without this, two users using the same key would resolve each other's
// operations (a cross-user information leak).
func idempotencyKeyFor(userID, key string) string {
	return userID + "|" + key
}

func (s *Service) validateTransition(vm *models.VM, opType models.OperationType) error {
	switch opType {
	case models.OperationStart:
		if vm.Status != models.VMStatusStopped && vm.Status != models.VMStatusError {
			return ErrInvalidState
		}
	case models.OperationStop:
		if vm.Status != models.VMStatusRunning {
			return ErrInvalidState
		}
	case models.OperationReboot:
		if vm.Status != models.VMStatusRunning {
			return ErrInvalidState
		}
	case models.OperationDelete:
		if vm.Status == models.VMStatusTerminated {
			return ErrInvalidState
		}
	}
	return nil
}

func (s *Service) GetOperation(ctx context.Context, userID, operationID string) (*models.VMOperation, error) {
	op, err := s.ops.GetByID(ctx, operationID)
	if err != nil {
		return nil, ErrNotFound
	}
	if op.VMID != "" {
		vm, err := s.vms.GetByID(ctx, op.VMID)
		if err == nil && vm.UserID != userID {
			return nil, ErrNotFound
		}
	}
	return op, nil
}

// NodeEndpoint returns the agent endpoint for a node ID.
func (s *Service) NodeEndpoint(ctx context.Context, nodeID string) (string, error) {
	if nodeID == "" {
		return "", ErrNotFound
	}
	node, err := s.nodes.GetByID(ctx, nodeID)
	if err != nil {
		return "", ErrNotFound
	}
	return node.AgentEndpoint, nil
}

// SuspendUserVMs gracefully shuts down all of a user's running VMs (payment
// overdue). Already-stopped VMs are skipped.
func (s *Service) SuspendUserVMs(ctx context.Context, userID string) error {
	vms, err := s.ListVMs(ctx, userID)
	if err != nil {
		return err
	}
	for _, vm := range vms {
		if vm.Status != models.VMStatusRunning {
			continue
		}
		if _, err := s.NewOperation(ctx, userID, vm.ID, models.OperationStop, ""); err != nil {
			// A VM that just changed state may reject the transition; the
			// suspension deadline still applies, so do not fail the batch.
			continue
		}
	}
	return nil
}

// TerminateUserVMs schedules termination of all of a user's VMs (grace period
// expired or subscription canceled).
func (s *Service) TerminateUserVMs(ctx context.Context, userID string) error {
	vms, err := s.ListVMs(ctx, userID)
	if err != nil {
		return err
	}
	for _, vm := range vms {
		if vm.Status == models.VMStatusTerminated {
			continue
		}
		if _, err := s.NewOperation(ctx, userID, vm.ID, models.OperationTerminate, ""); err != nil {
			continue
		}
	}
	return nil
}

// WaitForOperation polls until the operation reaches a terminal state or the
// context expires.
func (s *Service) WaitForOperation(ctx context.Context, operationID string) (*models.VMOperation, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		op, err := s.ops.GetByID(ctx, operationID)
		if err != nil {
			return nil, err
		}
		if op.Status == models.OperationSucceeded || op.Status == models.OperationFailed {
			return op, nil
		}
		select {
		case <-ctx.Done():
			return op, ctx.Err()
		case <-ticker.C:
		}
	}
}
