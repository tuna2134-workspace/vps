// Package cluster manages clusters, nodes, and agent heartbeats.
package cluster

import (
	"context"
	"errors"
	"time"

	"github.com/tuna2134/vps/internal/controlplane/audit"
	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/repositories"
)

var (
	ErrConflict = errors.New("conflict")
	ErrNotFound = errors.New("not found")
)

type Service struct {
	clusters  *repositories.ClusterRepository
	nodes     *repositories.NodeRepository
	pools     *repositories.StoragePoolRepository
	audit     *audit.Service
	degradedAfter time.Duration
	offlineAfter  time.Duration
}

func NewService(
	clusters *repositories.ClusterRepository,
	nodes *repositories.NodeRepository,
	pools *repositories.StoragePoolRepository,
	auditSvc *audit.Service,
	degradedAfter, offlineAfter time.Duration,
) *Service {
	return &Service{clusters: clusters, nodes: nodes, pools: pools, audit: auditSvc,
		degradedAfter: degradedAfter, offlineAfter: offlineAfter}
}

func (s *Service) CreateCluster(ctx context.Context, name, description string) (*models.Cluster, error) {
	c, err := s.clusters.Create(ctx, name, description)
	if err != nil {
		if errors.Is(err, repositories.ErrConflict) {
			return nil, ErrConflict
		}
		return nil, err
	}
	_ = s.audit.Record(ctx, audit.Event{
		Action:       "cluster.create",
		ResourceType: "cluster",
		ResourceID:   c.ID,
	})
	return c, nil
}

func (s *Service) ListClusters(ctx context.Context) ([]models.Cluster, error) {
	return s.clusters.List(ctx)
}

func (s *Service) GetCluster(ctx context.Context, id string) (*models.Cluster, error) {
	return s.clusters.GetByID(ctx, id)
}

// RegisterNode creates a node record. If a node with the same name exists it
// is returned as an error to prevent duplicate agent registration.
func (s *Service) RegisterNode(ctx context.Context, clusterID, name, endpoint string) (*models.Node, error) {
	n, err := s.nodes.Create(ctx, &models.Node{
		ClusterID:     clusterID,
		Name:          name,
		AgentEndpoint: endpoint,
		Status:        models.NodeStatusOffline,
	})
	if err != nil {
		if errors.Is(err, repositories.ErrConflict) {
			return nil, ErrConflict
		}
		return nil, err
	}
	_ = s.audit.Record(ctx, audit.Event{
		Action:       "node.register",
		ResourceType: "node",
		ResourceID:   n.ID,
	})
	return n, nil
}

func (s *Service) ListNodes(ctx context.Context, clusterID string) ([]models.Node, error) {
	return s.nodes.ListByCluster(ctx, clusterID)
}

func (s *Service) GetNode(ctx context.Context, id string) (*models.Node, error) {
	return s.nodes.GetByID(ctx, id)
}

// Heartbeat updates node capacity/liveness from the agent-reported metrics.
func (s *Service) Heartbeat(ctx context.Context, agentName string, m *models.NodeMetrics) error {
	node, err := s.nodes.GetByName(ctx, agentName)
	if err != nil {
		return err
	}
	memCapacityMB := m.MemoryTotalBytes / (1024 * 1024)
	storageCapacityGB := m.StorageTotalBytes / (1024 * 1024 * 1024)
	memUsage := float64(0)
	if m.MemoryTotalBytes > 0 {
		memUsage = float64(m.MemoryTotalBytes-m.MemoryFreeBytes) / float64(m.MemoryTotalBytes) * 100
	}
	return s.nodes.UpdateHeartbeat(ctx, node.ID, m.CPUCapacity,
		memCapacityMB, storageCapacityGB,
		m.CPUUsagePercent, memUsage)
}

// ReconcileStatuses flags stale nodes as degraded/offline.
func (s *Service) ReconcileStatuses(ctx context.Context) error {
	_, err := s.nodes.ReconcileStatuses(ctx, s.degradedAfter, s.offlineAfter)
	return err
}

// ReportStoragePool upserts a node's storage pool status from the agent.
func (s *Service) ReportStoragePool(ctx context.Context, nodeID string, pool *models.StoragePool) error {
	return s.pools.UpsertFromAgent(ctx, nodeID, pool)
}

func (s *Service) ListStoragePools(ctx context.Context, nodeID string) ([]models.StoragePool, error) {
	return s.pools.ListByNode(ctx, nodeID)
}