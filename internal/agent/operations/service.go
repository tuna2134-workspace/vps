package operations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Result is the outcome of an idempotent operation.
type Result struct {
	// State is succeeded or failed.
	State State
	// Error is set when the operation (or a replayed one) failed.
	Error error
}

// Succeeded reports whether the operation completed successfully. A replayed
// successful operation is also reported as succeeded.
func (r *Result) Succeeded() bool { return r.State == StateSucceeded }

// Service executes mutating operations exactly once per operation id, even
// across agent restarts, using the durable Store.
type Service struct {
	store Store
}

// NewService builds an operation service backed by store.
func NewService(store Store) *Service {
	return &Service{store: store}
}

// Execute runs fn exactly once for a given operation id. On replay of a
// succeeded operation it returns the recorded success without calling fn; a
// running operation is reported as running (never double-executed); a request
// with the same id but a different hash is rejected.
func (s *Service) Execute(ctx context.Context, opID, opType, vmID, requestHash string, fn func(context.Context) error) (*Result, error) {
	if opID == "" {
		// No idempotency key supplied (legacy callers): execute directly.
		if err := fn(ctx); err != nil {
			return &Result{State: StateFailed, Error: err}, nil
		}
		return &Result{State: StateSucceeded}, nil
	}

	now := time.Now().UTC()
	err := s.store.Create(ctx, &Operation{
		ID:          opID,
		Type:        opType,
		VMID:        vmID,
		RequestHash: requestHash,
		State:       StatePending,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		if !errors.Is(err, ErrConflict) {
			return nil, fmt.Errorf("record operation: %w", err)
		}
		// A concurrent call created the operation first; adopt its state.
		return s.replay(ctx, opID, requestHash)
	}

	if err := s.store.MarkRunning(ctx, opID); err != nil {
		return nil, fmt.Errorf("mark operation running: %w", err)
	}

	execErr := fn(ctx)
	if execErr != nil {
		if merr := s.store.MarkFailed(ctx, opID, execErr.Error()); merr != nil {
			return nil, fmt.Errorf("mark operation failed: %w", merr)
		}
		return &Result{State: StateFailed, Error: execErr}, nil
	}
	if merr := s.store.MarkSucceeded(ctx, opID); merr != nil {
		return nil, fmt.Errorf("mark operation succeeded: %w", merr)
	}
	return &Result{State: StateSucceeded}, nil
}

// replay handles a retried operation that already exists.
func (s *Service) replay(ctx context.Context, opID, requestHash string) (*Result, error) {
	op, err := s.store.Get(ctx, opID)
	if err != nil {
		return nil, fmt.Errorf("get operation: %w", err)
	}
	if op.RequestHash != requestHash {
		return nil, fmt.Errorf("operation %s was retried with a different request", opID)
	}
	switch op.State {
	case StateSucceeded:
		return &Result{State: StateSucceeded}, nil
	case StateFailed:
		return &Result{State: StateFailed, Error: errors.New(op.Error)}, nil
	case StateRunning, StatePending:
		// An earlier delivery is still executing; never double-execute.
		return &Result{State: StateRunning, Error: errors.New("operation already in progress")}, nil
	default:
		return nil, fmt.Errorf("operation %s has unknown state %q", opID, op.State)
	}
}

// HashRequest returns a stable SHA-256 digest of a request. The input must be
// a deterministic encoding of the request; proto marshal is not guaranteed
// stable, so callers should hash a canonical form.
func HashRequest(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
