// Package operations implements agent-side operation idempotency. The agent
// records every mutating RPC (create/delete/start/stop/force-stop/reboot)
// against a durable SQLite store so that a retried RPC never executes twice
// and always returns the same result.
package operations

import (
	"context"
	"time"
)

// State is the lifecycle state of an agent operation.
type State string

const (
	StatePending   State = "pending"
	StateRunning   State = "running"
	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
)

// Operation is a recorded, deduplicatable agent RPC.
type Operation struct {
	ID          string
	Type        string
	VMID        string
	RequestHash string
	State       State
	Error       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Store is the durable operation store. Implementations must be safe for
// concurrent use.
type Store interface {
	// Get returns the operation or ErrNotFound.
	Get(ctx context.Context, id string) (*Operation, error)
	// Create inserts a pending operation. It returns ErrConflict if an
	// operation with the same id already exists.
	Create(ctx context.Context, op *Operation) error
	MarkRunning(ctx context.Context, id string) error
	MarkSucceeded(ctx context.Context, id string) error
	MarkFailed(ctx context.Context, id, message string) error
}
