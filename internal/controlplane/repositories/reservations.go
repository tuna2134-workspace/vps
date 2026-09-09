package repositories

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/tuna2134/vps/internal/controlplane/models"
)

// ReservationState is the lifecycle state of a resource reservation.
type ReservationState string

const (
	ReservationPending   ReservationState = "pending"
	ReservationCommitted ReservationState = "committed"
	ReservationReleased  ReservationState = "released"
)

// ResourceReservationRepository reserves node capacity for a VM before the
// VM record is committed. The node row is locked (SELECT ... FOR UPDATE) so
// concurrent provisioning can never over-allocate CPU, memory, or storage.
type ResourceReservationRepository struct {
	db DBTX
}

func NewResourceReservationRepository(db DBTX) *ResourceReservationRepository {
	return &ResourceReservationRepository{db: db}
}

const reservationCols = `id, node_id, vm_id, cpu_vcpu, memory_bytes, storage_bytes, state, created_at, updated_at`

func scanReservation(row pgx.Row) (*models.ResourceReservation, error) {
	var r models.ResourceReservation
	if err := row.Scan(&r.ID, &r.NodeID, &r.VMID, &r.CPUVCPU, &r.MemoryBytes, &r.StorageBytes,
		&r.State, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

// Reserve atomically selects a node that fits the request and inserts a
// reservation for it. Capacity is computed from the locked node, the sum of
// live VM allocations, and active reservations.
//
//   - CPU:   allocated_vcpu + reserved + requested <= physical_cores * ratio
//   - Memory: allocated + reserved + requested <= memory_capacity
//   - Storage: reserved + requested <= storage_free_bytes
func (r *ResourceReservationRepository) Reserve(ctx context.Context, req models.ReservationRequest) (*models.Node, *models.ResourceReservation, error) {
	tx, err := beginTx(ctx, r.db)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	nodes, err := r.lockCandidateNodes(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	for i := range nodes {
		n := &nodes[i]
		if req.ClusterID != "" && n.ClusterID != req.ClusterID {
			continue
		}
		if !r.nodeFits(ctx, tx, n, req) {
			continue
		}
		res, err := insertReservation(ctx, tx, n.ID, req)
		if err != nil {
			return nil, nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, nil, fmt.Errorf("commit reservation: %w", err)
		}
		return n, res, nil
	}
	return nil, nil, ErrNoCapacity
}

// lockCandidateNodes returns healthy/degraded nodes in a deterministic order,
// each locked for the duration of the transaction.
func (r *ResourceReservationRepository) lockCandidateNodes(ctx context.Context, tx pgx.Tx) ([]models.Node, error) {
	rows, err := tx.Query(ctx, `
		SELECT `+nodeCols+`
		FROM nodes
		WHERE status IN ('healthy', 'degraded')
		ORDER BY name
		FOR UPDATE`)
	if err != nil {
		return nil, fmt.Errorf("lock nodes: %w", err)
	}
	defer rows.Close()
	var out []models.Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

func (r *ResourceReservationRepository) nodeFits(ctx context.Context, tx pgx.Tx, n *models.Node, req models.ReservationRequest) bool {
	// CPU
	var allocatedVCPU, allocatedMemBytes int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(vcpu), 0), COALESCE(SUM(memory_mb), 0) * 1024 * 1024
		FROM vms
		WHERE node_id = $1 AND deleted_at IS NULL AND status <> 'terminated'`, n.ID).
		Scan(&allocatedVCPU, &allocatedMemBytes); err != nil {
		return false
	}
	var reservedVCPU, reservedMem, reservedStorage int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(cpu_vcpu), 0), COALESCE(SUM(memory_bytes), 0), COALESCE(SUM(storage_bytes), 0)
		FROM resource_reservations
		WHERE node_id = $1 AND state IN ('pending', 'committed')`, n.ID).
		Scan(&reservedVCPU, &reservedMem, &reservedStorage); err != nil {
		return false
	}

	cores := n.PhysicalCores
	if cores <= 0 {
		cores = n.CPUCapacity
	}
	ratio := n.CPUOvercommitRatio
	if ratio <= 0 {
		ratio = 1.0
	}
	cpuCapacity := float64(cores) * ratio
	if float64(allocatedVCPU+reservedVCPU)+float64(req.VCPU) > cpuCapacity {
		return false
	}
	if allocatedMemBytes+reservedMem+req.MemoryBytes > n.MemoryCapacityMB*1024*1024 {
		return false
	}
	if reservedStorage+req.StorageBytes > n.StorageFreeBytes {
		return false
	}
	return true
}

func insertReservation(ctx context.Context, tx pgx.Tx, nodeID string, req models.ReservationRequest) (*models.ResourceReservation, error) {
	row := tx.QueryRow(ctx, `
		INSERT INTO resource_reservations (node_id, vm_id, cpu_vcpu, memory_bytes, storage_bytes, state)
		VALUES ($1, $2, $3, $4, $5, 'pending')
		RETURNING `+reservationCols,
		nodeID, req.VMID, req.VCPU, req.MemoryBytes, req.StorageBytes)
	res, err := scanReservation(row)
	if err != nil {
		return nil, fmt.Errorf("insert reservation: %w", err)
	}
	return res, nil
}

// Commit marks a reservation committed after the VM is provisioned.
func (r *ResourceReservationRepository) Commit(ctx context.Context, vmID string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE resource_reservations SET state = 'committed', updated_at = now()
		WHERE vm_id = $1`, vmID)
	if err != nil {
		return fmt.Errorf("commit reservation: %w", err)
	}
	_ = tag
	return nil
}

// Release frees a reservation (provisioning failed or VM removed).
func (r *ResourceReservationRepository) Release(ctx context.Context, vmID string) error {
	_, err := r.db.Exec(ctx, `
		DELETE FROM resource_reservations WHERE vm_id = $1`, vmID)
	if err != nil {
		return fmt.Errorf("release reservation: %w", err)
	}
	return nil
}

var ErrNoCapacity = errors.New("no node has sufficient capacity")
