package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tuna2134/vps/internal/controlplane/models"
)

type ClusterRepository struct {
	db DBTX
}

func NewClusterRepository(db DBTX) *ClusterRepository {
	return &ClusterRepository{db: db}
}

const clusterCols = `id, name, description, status, created_at, updated_at`

func scanCluster(row pgx.Row) (*models.Cluster, error) {
	var c models.Cluster
	err := row.Scan(&c.ID, &c.Name, &c.Description, &c.Status, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *ClusterRepository) Create(ctx context.Context, name, description string) (*models.Cluster, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO clusters (name, description) VALUES ($1, $2) RETURNING `+clusterCols,
		name, description)
	c, err := scanCluster(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("create cluster: %w", ErrConflict)
		}
		return nil, fmt.Errorf("create cluster: %w", err)
	}
	return c, nil
}

func (r *ClusterRepository) GetByID(ctx context.Context, id string) (*models.Cluster, error) {
	row := r.db.QueryRow(ctx, `SELECT `+clusterCols+` FROM clusters WHERE id = $1`, id)
	c, err := scanCluster(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get cluster: %w", err)
	}
	return c, nil
}

func (r *ClusterRepository) List(ctx context.Context) ([]models.Cluster, error) {
	rows, err := r.db.Query(ctx, `SELECT `+clusterCols+` FROM clusters ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list clusters: %w", err)
	}
	defer rows.Close()
	var out []models.Cluster
	for rows.Next() {
		c, err := scanCluster(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

type NodeRepository struct {
	db DBTX
}

func NewNodeRepository(db DBTX) *NodeRepository {
	return &NodeRepository{db: db}
}

const nodeCols = `id, cluster_id, name, agent_endpoint, status, cpu_capacity, memory_capacity_mb,
	storage_capacity_gb, cpu_usage_percent, memory_usage_percent, last_heartbeat, created_at, updated_at`

func scanNode(row pgx.Row) (*models.Node, error) {
	var n models.Node
	var cpuUsage, memUsage float32
	if err := row.Scan(&n.ID, &n.ClusterID, &n.Name, &n.AgentEndpoint, &n.Status,
		&n.CPUCapacity, &n.MemoryCapacityMB, &n.StorageCapacityGB,
		&cpuUsage, &memUsage, &n.LastHeartbeat, &n.CreatedAt, &n.UpdatedAt); err != nil {
		return nil, err
	}
	n.CPUUsagePercent = float64(cpuUsage)
	n.MemoryUsagePercent = float64(memUsage)
	return &n, nil
}

func (r *NodeRepository) Create(ctx context.Context, n *models.Node) (*models.Node, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO nodes (cluster_id, name, agent_endpoint, status,
			cpu_capacity, memory_capacity_mb, storage_capacity_gb)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+nodeCols,
		n.ClusterID, n.Name, n.AgentEndpoint, n.Status,
		n.CPUCapacity, n.MemoryCapacityMB, n.StorageCapacityGB)
	created, err := scanNode(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("create node: %w", ErrConflict)
		}
		return nil, fmt.Errorf("create node: %w", err)
	}
	return created, nil
}

func (r *NodeRepository) GetByID(ctx context.Context, id string) (*models.Node, error) {
	row := r.db.QueryRow(ctx, `SELECT `+nodeCols+` FROM nodes WHERE id = $1`, id)
	n, err := scanNode(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get node: %w", err)
	}
	return n, nil
}

func (r *NodeRepository) GetByName(ctx context.Context, name string) (*models.Node, error) {
	row := r.db.QueryRow(ctx, `SELECT `+nodeCols+` FROM nodes WHERE name = $1`, name)
	n, err := scanNode(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get node by name: %w", err)
	}
	return n, nil
}

func (r *NodeRepository) ListByCluster(ctx context.Context, clusterID string) ([]models.Node, error) {
	rows, err := r.db.Query(ctx, `SELECT `+nodeCols+` FROM nodes WHERE cluster_id = $1 ORDER BY name`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
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

func (r *NodeRepository) ListHealthy(ctx context.Context) ([]models.Node, error) {
	rows, err := r.db.Query(ctx, `SELECT `+nodeCols+` FROM nodes WHERE status IN ('healthy', 'degraded') ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list healthy nodes: %w", err)
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

// UpdateHeartbeat records agent capacity and liveness.
func (r *NodeRepository) UpdateHeartbeat(ctx context.Context, nodeID string, cpuCapacity int, memoryCapacityMB, storageCapacityGB int64, cpuUsage, memUsage float64) error {
	_, err := r.db.Exec(ctx, `
		UPDATE nodes SET
			cpu_capacity = $2,
			memory_capacity_mb = $3,
			storage_capacity_gb = $4,
			cpu_usage_percent = $5,
			memory_usage_percent = $6,
			last_heartbeat = now(),
			status = 'healthy',
			updated_at = now()
		WHERE id = $1`, nodeID, cpuCapacity, memoryCapacityMB, storageCapacityGB, cpuUsage, memUsage)
	if err != nil {
		return fmt.Errorf("update node heartbeat: %w", err)
	}
	return nil
}

// ReconcileStatuses flags nodes as degraded/offline based on heartbeat age.
func (r *NodeRepository) ReconcileStatuses(ctx context.Context, degradedAfter, offlineAfter time.Duration) (int64, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE nodes SET
			status = CASE
				WHEN last_heartbeat IS NULL OR last_heartbeat < now() - $2::interval THEN 'offline'
				WHEN last_heartbeat < now() - $1::interval THEN 'degraded'
				ELSE status
			END,
			updated_at = now()
		WHERE status <> 'disabled'
		  AND (last_heartbeat IS NULL OR last_heartbeat < now() - $2::interval
		    OR (status <> 'degraded' AND last_heartbeat < now() - $1::interval))`,
		degradedAfter.String(), offlineAfter.String())
	if err != nil {
		return 0, fmt.Errorf("reconcile node statuses: %w", err)
	}
	return tag.RowsAffected(), nil
}

type StoragePoolRepository struct {
	db DBTX
}

func NewStoragePoolRepository(db DBTX) *StoragePoolRepository {
	return &StoragePoolRepository{db: db}
}

func (r *StoragePoolRepository) UpsertFromAgent(ctx context.Context, nodeID string, p *models.StoragePool) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO storage_pools (node_id, name, path, type, total_bytes, used_bytes)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (node_id, name) DO UPDATE SET
			path = EXCLUDED.path,
			type = EXCLUDED.type,
			total_bytes = EXCLUDED.total_bytes,
			used_bytes = EXCLUDED.used_bytes,
			status = 'active',
			updated_at = now()`,
		nodeID, p.Name, p.Path, p.Type, p.TotalBytes, p.UsedBytes)
	if err != nil {
		return fmt.Errorf("upsert storage pool: %w", err)
	}
	return nil
}

func (r *StoragePoolRepository) ListByNode(ctx context.Context, nodeID string) ([]models.StoragePool, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, node_id, name, path, type, total_bytes, used_bytes, status
		FROM storage_pools WHERE node_id = $1`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list storage pools: %w", err)
	}
	defer rows.Close()
	var out []models.StoragePool
	for rows.Next() {
		var p models.StoragePool
		if err := rows.Scan(&p.ID, &p.NodeID, &p.Name, &p.Path, &p.Type, &p.TotalBytes, &p.UsedBytes, &p.Status); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}