// Package scheduler selects an agent node for a new VM based on resource
// capacity and health. It is deliberately decoupled from the Agent gRPC layer.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"

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

// Scheduler picks the most suitable node for a placement request.
type Scheduler struct {
	nodes *repositories.NodeRepository
	vms   *repositories.VMRepository
}

func New(nodes *repositories.NodeRepository, vms *repositories.VMRepository) *Scheduler {
	return &Scheduler{nodes: nodes, vms: vms}
}

// Select chooses a node. The decision considers CPU, memory, storage capacity
// and node health. The scoring function is isolated so affinity/anti-affinity,
// geographic location, and overcommit policies can be added later.
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
		fit, score := s.score(ctx, n, req)
		if fit {
			candidates = append(candidates, candidate{node: n, score: score})
		}
	}
	if len(candidates) == 0 {
		return nil, ErrNoCapacity
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score < candidates[j].score // lowest utilization wins
	})
	return candidates[0].node, nil
}

// score returns whether the node fits and a utilization score (lower is
// better). Extend this with overcommit ratios etc. as needed.
func (s *Scheduler) score(ctx context.Context, n *models.Node, req Request) (bool, float64) {
	if n.Status != models.NodeStatusHealthy && n.Status != models.NodeStatusDegraded {
		return false, 0
	}
	if n.CPUCapacity <= 0 {
		return false, 0
	}

	memoryMB := int64(req.MemoryMB)
	if memoryMB <= 0 || memoryMB > n.MemoryCapacityMB {
		return false, 0
	}
	if int64(req.DiskGB) <= 0 || int64(req.DiskGB) > n.StorageCapacityGB {
		return false, 0
	}

	cpuUtil := n.CPUUsagePercent
	memUtil := n.MemoryUsagePercent
	if memUtil <= 0 {
		memUtil = float64(memoryMB) / float64(n.MemoryCapacityMB) * 100
	}

	// A node is disqualified if adding this VM would exceed its memory.
	usedMemMB := float64(n.MemoryCapacityMB) * memUtil / 100
	if usedMemMB+float64(memoryMB) > float64(n.MemoryCapacityMB) {
		return false, 0
	}

	score := cpuUtil*0.4 + memUtil*0.4
	if n.Status == models.NodeStatusDegraded {
		score += 100 // prefer healthy nodes
	}
	return true, score
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
