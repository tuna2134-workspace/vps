package integration

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/repositories"
)

func gb(n int64) int64 { return n * 1024 * 1024 * 1024 }

func randUUID(t *testing.T) string {
	t.Helper()
	return uuid.NewString()
}

func newTestNode(t *testing.T, ctx context.Context, name string) (*models.Node, string) {
	t.Helper()
	cluster, err := repos.Clusters.Create(ctx, "cluster-"+randSuffix(), "")
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	node, err := repos.Nodes.Create(ctx, &models.Node{
		ClusterID: cluster.ID, Name: name, AgentEndpoint: "agent-a:9001", Status: models.NodeStatusOffline,
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	// 8 physical cores, ratio 4 -> 32 vCPU capacity; 1TB free storage.
	if err := repos.Nodes.UpdateHeartbeat(ctx, node.ID, 8, 32768, 1000, gb(1000), 0, 0); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if err := repos.Nodes.SetOvercommitRatio(ctx, node.ID, 4.0); err != nil {
		t.Fatalf("set ratio: %v", err)
	}
	// Re-read with the updated ratio.
	n, err := repos.Nodes.GetByID(ctx, node.ID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	return n, cluster.ID
}

// TestReserveCPUOvercommitReject verifies: 8 cores, ratio 4 -> capacity 32.
// With 28 vCPU already accounted, a request for 8 must be rejected.
func TestReserveCPUOvercommitReject(t *testing.T) {
	ctx := newContext(t)
	node, _ := newTestNode(t, ctx, "cpu-node-"+randSuffix())

	res := repositories.NewResourceReservationRepository(testPool)

	// Seed 28 allocated vCPU via VM records.
	vmStore := repos.VMs
	_ = vmStore
	mac := uniqueMAC()
	user := newTestUser(t, ctx)
	for i := 0; i < 28; i++ {
		if _, err := repos.VMs.Create(ctx, &models.VM{
			ID: randUUID(t), UserID: user.ID, Name: "vm", Hostname: "vm", Status: models.VMStatusRunning,
			NodeID: node.ID, VCPU: 1, MemoryMB: 1024, DiskGB: 10, MACAddress: mac, InstanceID: randUUID(t),
		}); err != nil {
			t.Fatalf("seed vm: %v", err)
		}
		mac = uniqueMAC()
	}

	// A request for 8 vCPU on top of 28 allocated must be rejected (28+8 > 32).
	if _, _, err := res.Reserve(ctx, models.ReservationRequest{
		VMID: randUUID(t), ClusterID: node.ClusterID, VCPU: 8, MemoryBytes: gb(1), StorageBytes: gb(10),
	}); err == nil {
		t.Fatal("expected rejection: 28 allocated + 8 requested exceeds capacity 32")
	}

	// A request for 4 vCPU fits (28+4 <= 32).
	_, r, err := res.Reserve(ctx, models.ReservationRequest{
		VMID: randUUID(t), ClusterID: node.ClusterID, VCPU: 4, MemoryBytes: gb(1), StorageBytes: gb(10),
	})
	if err != nil {
		t.Fatalf("expected 4 vCPU to fit, got %v", err)
	}
	if r.NodeID != node.ID {
		t.Fatalf("reservation on wrong node: %s", r.NodeID)
	}
}

// TestReserveStorageReject verifies: 1TB free, reservation 900GB, request
// 200GB must be rejected.
func TestReserveStorageReject(t *testing.T) {
	ctx := newContext(t)
	node, _ := newTestNode(t, ctx, "storage-node-"+randSuffix())
	res := repositories.NewResourceReservationRepository(testPool)

	// Reserve 900GB first.
	if _, _, err := res.Reserve(ctx, models.ReservationRequest{
		VMID: randUUID(t), ClusterID: node.ClusterID, VCPU: 1, MemoryBytes: gb(1), StorageBytes: gb(900),
	}); err != nil {
		t.Fatalf("reserve 900GB: %v", err)
	}
	// Now 200GB must not fit (900 reserved + 200 > 1000 free).
	if _, _, err := res.Reserve(ctx, models.ReservationRequest{
		VMID: randUUID(t), ClusterID: node.ClusterID, VCPU: 1, MemoryBytes: gb(1), StorageBytes: gb(200),
	}); err == nil {
		t.Fatal("expected storage rejection: 900 reserved + 200 requested > 1000 free")
	}
	// 50GB fits.
	if _, _, err := res.Reserve(ctx, models.ReservationRequest{
		VMID: randUUID(t), ClusterID: node.ClusterID, VCPU: 1, MemoryBytes: gb(1), StorageBytes: gb(50),
	}); err != nil {
		t.Fatalf("expected 50GB to fit, got %v", err)
	}
}

// TestReserveConcurrentNoOverallocation verifies concurrent reservations never
// exceed CPU capacity.
func TestReserveConcurrentNoOverallocation(t *testing.T) {
	ctx := newContext(t)
	node, clusterID := newTestNode(t, ctx, "conc-node-"+randSuffix())
	res := repositories.NewResourceReservationRepository(testPool)

	const vms = 20 // 20 x 2 vCPU = 40, but capacity is 32
	var wg sync.WaitGroup
	reserved := make([]bool, vms)
	var mu sync.Mutex
	success := 0

	for i := 0; i < vms; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vmID := randUUID(t)
			_, _, err := res.Reserve(context.Background(), models.ReservationRequest{
				VMID: vmID, ClusterID: clusterID, VCPU: 2, MemoryBytes: gb(1), StorageBytes: gb(1),
			})
			mu.Lock()
			defer mu.Unlock()
			reserved[success] = err == nil
			if err == nil {
				success++
			}
		}()
	}
	wg.Wait()

	if success > 16 {
		t.Fatalf("over-allocated: %d reservations succeeded, capacity is 16 (32 vCPU / 2)", success)
	}
	if success == 0 {
		t.Fatal("expected at least one reservation to succeed")
	}
	_ = node
}
