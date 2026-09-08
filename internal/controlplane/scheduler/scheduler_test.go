package scheduler

import (
	"context"
	"testing"

	"github.com/tuna2134/vps/internal/controlplane/models"
)

type fakeNodeRepo struct {
	nodes []models.Node
}

func (f *fakeNodeRepo) ListHealthy(ctx context.Context) ([]models.Node, error) {
	return f.nodes, nil
}

type fakeVMRepo struct{}

func (f *fakeVMRepo) CountByNode(ctx context.Context, nodeID string) (int, error) { return 0, nil }

func TestSelectPicksNodeWithCapacity(t *testing.T) {
	s := New(&fakeNodeRepo{nodes: []models.Node{
		{ID: "a", Status: models.NodeStatusHealthy, CPUCapacity: 8, MemoryCapacityMB: 8192, StorageCapacityGB: 100, CPUUsagePercent: 90},
		{ID: "b", Status: models.NodeStatusHealthy, CPUCapacity: 8, MemoryCapacityMB: 8192, StorageCapacityGB: 100, CPUUsagePercent: 10},
	}}, &fakeVMRepo{})

	req := Request{VCPU: 2, MemoryMB: 2048, DiskGB: 20}
	node, err := s.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if node.ID != "b" {
		t.Errorf("expected least-loaded node b, got %s", node.ID)
	}
}

func TestSelectPrefersHealthy(t *testing.T) {
	s := New(&fakeNodeRepo{nodes: []models.Node{
		{ID: "degraded", Status: models.NodeStatusDegraded, CPUCapacity: 8, MemoryCapacityMB: 8192, StorageCapacityGB: 100, CPUUsagePercent: 5},
		{ID: "healthy", Status: models.NodeStatusHealthy, CPUCapacity: 8, MemoryCapacityMB: 8192, StorageCapacityGB: 100, CPUUsagePercent: 50},
	}}, &fakeVMRepo{})

	node, err := s.Select(context.Background(), Request{VCPU: 1, MemoryMB: 1024, DiskGB: 10})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if node.ID != "healthy" {
		t.Errorf("expected healthy node, got %s", node.ID)
	}
}

func TestSelectNoCapacity(t *testing.T) {
	s := New(&fakeNodeRepo{nodes: []models.Node{
		{ID: "small", Status: models.NodeStatusHealthy, CPUCapacity: 1, MemoryCapacityMB: 512, StorageCapacityGB: 10},
	}}, &fakeVMRepo{})

	if _, err := s.Select(context.Background(), Request{VCPU: 8, MemoryMB: 8192, DiskGB: 100}); err != ErrNoCapacity {
		t.Errorf("expected ErrNoCapacity, got %v", err)
	}
}

func TestSelectNoNodes(t *testing.T) {
	s := New(&fakeNodeRepo{}, &fakeVMRepo{})
	if _, err := s.Select(context.Background(), Request{VCPU: 1, MemoryMB: 512, DiskGB: 5}); err != ErrNoNodes {
		t.Errorf("expected ErrNoNodes, got %v", err)
	}
}

func TestSelectClusterConstraint(t *testing.T) {
	s := New(&fakeNodeRepo{nodes: []models.Node{
		{ID: "c1", ClusterID: "cluster-1", Status: models.NodeStatusHealthy, CPUCapacity: 8, MemoryCapacityMB: 8192, StorageCapacityGB: 100, CPUUsagePercent: 10},
		{ID: "c2", ClusterID: "cluster-2", Status: models.NodeStatusHealthy, CPUCapacity: 8, MemoryCapacityMB: 8192, StorageCapacityGB: 100, CPUUsagePercent: 5},
	}}, &fakeVMRepo{})

	node, err := s.Select(context.Background(), Request{VCPU: 1, MemoryMB: 512, DiskGB: 5, ClusterID: "cluster-1"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if node.ID != "c1" {
		t.Errorf("expected node in cluster-1, got %s", node.ID)
	}
}
