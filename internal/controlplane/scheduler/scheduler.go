// Package scheduler selects an agent node for a new VM based on CPU, memory,
// and storage accounting and node health. Selection happens atomically with a
// resource reservation so concurrent provisioning can never over-allocate.
package scheduler

import (
	"context"
	"errors"
	"fmt"

	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/repositories"
)

var (
	ErrNoCapacity = errors.New("no node has sufficient capacity")
	ErrNoNodes    = errors.New("no healthy nodes available")
)

// Request describes the resources a new VM needs.
type Request struct {
	VCPU     int
	MemoryMB int
	DiskGB   int
	// ClusterID optionally constrains placement to a single cluster.
	ClusterID string
}

// NodeSource lists candidate nodes for placement.
type NodeSource interface {
	ListHealthy(ctx context.Context) ([]models.Node, error)
}

// VMCounter counts VMs on a node (used for reporting).
type VMCounter interface {
	CountByNode(ctx context.Context, nodeID string) (int, error)
}

// Scheduler picks the most suitable node for a placement request.
type Scheduler struct {
	nodes        NodeSource
	vms          VMCounter
	reservations *repositories.ResourceReservationRepository
}

// New builds a scheduler. res must be non-nil to enable atomic reservation.
func New(nodes NodeSource, vms VMCounter, res *repositories.ResourceReservationRepository) *Scheduler {
	return &Scheduler{nodes: nodes, vms: vms, reservations: res}
}

// Reserve atomically selects a node for the request and reserves its capacity
// for vmID. The reservation is released on provisioning failure and committed
// on success.
func (s *Scheduler) Reserve(ctx context.Context, vmID string, req Request) (*models.Node, error) {
	if s.reservations == nil {
		return nil, errors.New("scheduler has no reservation store")
	}
	node, _, err := s.reservations.Reserve(ctx, models.ReservationRequest{
		VMID:         vmID,
		ClusterID:    req.ClusterID,
		VCPU:         req.VCPU,
		MemoryBytes:  int64(req.MemoryMB) * 1024 * 1024,
		StorageBytes: int64(req.DiskGB) * 1024 * 1024 * 1024,
	})
	if err != nil {
		if errors.Is(err, repositories.ErrNoCapacity) {
			return nil, ErrNoCapacity
		}
		return nil, err
	}
	return node, nil
}

// Commit marks the reservation for vmID committed after provisioning.
func (s *Scheduler) Commit(ctx context.Context, vmID string) error {
	if s.reservations == nil {
		return nil
	}
	return s.reservations.Commit(ctx, vmID)
}

// Release frees the reservation for vmID (provisioning failed or VM removed).
func (s *Scheduler) Release(ctx context.Context, vmID string) error {
	if s.reservations == nil {
		return nil
	}
	return s.reservations.Release(ctx, vmID)
}

// Select chooses a node using the same accounting rules but WITHOUT reserving
// capacity. It is used for reporting/preview; CreateVM should call Reserve.
func (s *Scheduler) Select(ctx context.Context, req Request) (*models.Node, error) {
	nodes, err := s.nodes.ListHealthy(ctx)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, ErrNoNodes
	}

	type candidate struct {
		node  *models.Node
		score float64
	}
	var candidates []candidate

	for i := range nodes {
		n := &nodes[i]
		if req.ClusterID != "" && n.ClusterID != req.ClusterID {
			continue
		}
		if !s.fits(n, req) {
			continue
		}
		candidates = append(candidates, candidate{node: n, score: utilizationScore(n)})
	}
	if len(candidates) == 0 {
		return nil, ErrNoCapacity
	}

	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.score < best.score {
			best = c
		}
	}
	return best.node, nil
}

// fits applies the accounting rules without taking reservations into account
// (approximation used by Select).
func (s *Scheduler) fits(n *models.Node, req Request) bool {
	cores := n.PhysicalCores
	if cores <= 0 {
		cores = n.CPUCapacity
	}
	ratio := n.CPUOvercommitRatio
	if ratio <= 0 {
		ratio = 1.0
	}
	if float64(req.VCPU) > float64(cores)*ratio {
		return false
	}
	if int64(req.MemoryMB) > n.MemoryCapacityMB {
		return false
	}
	if int64(req.DiskGB)*1024*1024*1024 > n.StorageFreeBytes {
		return false
	}
	return n.Status == models.NodeStatusHealthy || n.Status == models.NodeStatusDegraded
}

// utilizationScore is a coarse scheduling score (lower is better). CPU usage
// is used only as a tie-breaker; placement capacity is decided by accounting.
func utilizationScore(n *models.Node) float64 {
	score := n.CPUUsagePercent*0.4 + n.MemoryUsagePercent*0.4
	if n.Status == models.NodeStatusDegraded {
		score += 100
	}
	return score
}

// UtilizationSummary is a helper for admin tooling.
func (s *Scheduler) UtilizationSummary(ctx context.Context, n *models.Node) (map[string]any, error) {
	count, err := s.vms.CountByNode(ctx, n.ID)
	if err != nil {
		return nil, fmt.Errorf("count vms: %w", err)
	}
	return map[string]any{
		"node_id":       n.ID,
		"vcpu_total":    n.CPUCapacity,
		"mem_total_mb":  n.MemoryCapacityMB,
		"disk_total_gb": n.StorageCapacityGB,
		"cpu_usage":     n.CPUUsagePercent,
		"mem_usage":     n.MemoryUsagePercent,
		"vms":           count,
	}, nil
}
