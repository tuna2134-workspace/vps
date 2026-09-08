package repositories

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/tuna2134/vps/internal/controlplane/models"
)

type VMRepository struct {
	db DBTX
}

func NewVMRepository(db DBTX) *VMRepository {
	return &VMRepository{db: db}
}

const vmCols = `id, user_id, plan_id, node_id, network_id, image_id, name, hostname, status,
	vcpu, memory_mb, disk_gb, mac_address, instance_id, created_at, updated_at, deleted_at`

func scanVM(row pgx.Row) (*models.VM, error) {
	var v models.VM
	var planID, nodeID, networkID, imageID *string
	err := row.Scan(&v.ID, &v.UserID, &planID, &nodeID, &networkID, &imageID,
		&v.Name, &v.Hostname, &v.Status, &v.VCPU, &v.MemoryMB, &v.DiskGB,
		&v.MACAddress, &v.InstanceID, &v.CreatedAt, &v.UpdatedAt, &v.DeletedAt)
	if err != nil {
		return nil, err
	}
	if planID != nil {
		v.PlanID = *planID
	}
	if nodeID != nil {
		v.NodeID = *nodeID
	}
	if networkID != nil {
		v.NetworkID = *networkID
	}
	if imageID != nil {
		v.ImageID = *imageID
	}
	return &v, nil
}

func (r *VMRepository) Create(ctx context.Context, v *models.VM) (*models.VM, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO vms (user_id, plan_id, network_id, image_id, name, hostname, status,
			vcpu, memory_mb, disk_gb, mac_address, instance_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING `+vmCols,
		v.UserID, v.PlanID, v.NetworkID, v.ImageID, v.Name, v.Hostname, v.Status,
		v.VCPU, v.MemoryMB, v.DiskGB, v.MACAddress, v.InstanceID)
	created, err := scanVM(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("create vm: %w", ErrConflict)
		}
		return nil, fmt.Errorf("create vm: %w", err)
	}
	return created, nil
}

func (r *VMRepository) GetByID(ctx context.Context, id string) (*models.VM, error) {
	row := r.db.QueryRow(ctx, `SELECT `+vmCols+` FROM vms WHERE id = $1`, id)
	v, err := scanVM(row)
	if isNoRowsOrInvalidUUID(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get vm: %w", err)
	}
	return v, nil
}

func (r *VMRepository) GetByInstanceID(ctx context.Context, instanceID string) (*models.VM, error) {
	row := r.db.QueryRow(ctx, `SELECT `+vmCols+` FROM vms WHERE instance_id = $1`, instanceID)
	v, err := scanVM(row)
	if isNoRowsOrInvalidUUID(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get vm by instance id: %w", err)
	}
	return v, nil
}

func (r *VMRepository) ListByUser(ctx context.Context, userID string) ([]models.VM, error) {
	rows, err := r.db.Query(ctx, `SELECT `+vmCols+` FROM vms WHERE user_id = $1 AND deleted_at IS NULL ORDER BY created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("list vms: %w", err)
	}
	defer rows.Close()
	var out []models.VM
	for rows.Next() {
		v, err := scanVM(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

func (r *VMRepository) ListByNode(ctx context.Context, nodeID string) ([]models.VM, error) {
	rows, err := r.db.Query(ctx, `SELECT `+vmCols+` FROM vms WHERE node_id = $1 AND deleted_at IS NULL`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list vms by node: %w", err)
	}
	defer rows.Close()
	var out []models.VM
	for rows.Next() {
		v, err := scanVM(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

func (r *VMRepository) UpdateStatus(ctx context.Context, id string, status models.VMStatus) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE vms SET status = $2, updated_at = now() WHERE id = $1 AND deleted_at IS NULL`, id, status)
	if err != nil {
		return fmt.Errorf("update vm status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *VMRepository) AssignNode(ctx context.Context, id, nodeID string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE vms SET node_id = $2, updated_at = now() WHERE id = $1`, id, nodeID)
	if err != nil {
		return fmt.Errorf("assign vm node: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SoftDelete marks a VM as terminated (kept for billing/audit history).
func (r *VMRepository) SoftDelete(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE vms SET status = 'terminated', deleted_at = now(), updated_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("soft delete vm: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Exists reports whether a MAC address is already assigned to a live VM.
func (r *VMRepository) Exists(ctx context.Context, mac string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM vms WHERE mac_address = $1 AND deleted_at IS NULL)`, mac).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check mac existence: %w", err)
	}
	return exists, nil
}

func (r *VMRepository) CountByNode(ctx context.Context, nodeID string) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `
		SELECT count(*) FROM vms WHERE node_id = $1 AND deleted_at IS NULL`, nodeID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count vms by node: %w", err)
	}
	return n, nil
}

type OperationRepository struct {
	db DBTX
}

func NewOperationRepository(db DBTX) *OperationRepository {
	return &OperationRepository{db: db}
}

const operationCols = `id, vm_id, operation_type, status, idempotency_key, error, created_at, updated_at, started_at, finished_at`

func scanOperation(row pgx.Row) (*models.VMOperation, error) {
	var o models.VMOperation
	var vmID *string
	err := row.Scan(&o.ID, &vmID, &o.OperationType, &o.Status, &o.IdempotencyKey, &o.Error,
		&o.CreatedAt, &o.UpdatedAt, &o.StartedAt, &o.FinishedAt)
	if err != nil {
		return nil, err
	}
	if vmID != nil {
		o.VMID = *vmID
	}
	return &o, nil
}

// Create registers a new operation, enforcing unique idempotency keys.
// Returns ErrConflict if the idempotency key already exists.
func (r *OperationRepository) Create(ctx context.Context, o *models.VMOperation) (*models.VMOperation, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO vm_operations (vm_id, operation_type, status, idempotency_key)
		VALUES ($1, $2, 'pending', $3)
		RETURNING `+operationCols,
		o.VMID, o.OperationType, o.IdempotencyKey)
	created, err := scanOperation(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("create operation: %w", ErrConflict)
		}
		return nil, fmt.Errorf("create operation: %w", err)
	}
	return created, nil
}

func (r *OperationRepository) GetByID(ctx context.Context, id string) (*models.VMOperation, error) {
	row := r.db.QueryRow(ctx, `SELECT `+operationCols+` FROM vm_operations WHERE id = $1`, id)
	o, err := scanOperation(row)
	if isNoRowsOrInvalidUUID(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get operation: %w", err)
	}
	return o, nil
}

func (r *OperationRepository) GetByIdempotencyKey(ctx context.Context, key string) (*models.VMOperation, error) {
	row := r.db.QueryRow(ctx, `SELECT `+operationCols+` FROM vm_operations WHERE idempotency_key = $1`, key)
	o, err := scanOperation(row)
	if isNoRowsOrInvalidUUID(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get operation by idempotency key: %w", err)
	}
	return o, nil
}

func (r *OperationRepository) MarkRunning(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE vm_operations SET status = 'running', started_at = coalesce(started_at, now()), updated_at = now()
		WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("mark operation running: %w", err)
	}
	return nil
}

func (r *OperationRepository) MarkSucceeded(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE vm_operations SET status = 'succeeded', finished_at = now(), updated_at = now()
		WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("mark operation succeeded: %w", err)
	}
	return nil
}

func (r *OperationRepository) MarkFailed(ctx context.Context, id, errMsg string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE vm_operations SET status = 'failed', error = $2, finished_at = now(), updated_at = now()
		WHERE id = $1`, id, errMsg)
	if err != nil {
		return fmt.Errorf("mark operation failed: %w", err)
	}
	return nil
}
