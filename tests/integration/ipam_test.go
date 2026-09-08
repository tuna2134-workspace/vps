package integration

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/networks"
	"github.com/tuna2134/vps/internal/controlplane/users"
)

// TestIPAMAllocation verifies IP allocation and release against PostgreSQL.
func TestIPAMAllocation(t *testing.T) {
	ctx := newContext(t)
	svc := networks.NewService(repos.Networks, repos.IPPools, nil)
	userSvc := newUserService(t)

	user, err := userSvc.Register(ctx, users.RegistrationRequest{
		Email: uniqueEmail("ipam"), Password: "password123",
		FirstName: "a", LastName: "b", LegalName: "a b",
		Country: "JP", PostalCode: "1", State: "x", City: "y", AddressLine1: "z",
	})
	if err != nil {
		t.Fatalf("register user: %v", err)
	}

	net, err := svc.CreateNetwork(ctx, &models.Network{
		Name:   "ipam-net-" + randSuffix(),
		Bridge: "br-ipam",
	})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	if _, err := svc.AddPool(ctx, &models.IPPool{
		NetworkID: net.ID,
		CIDR:      "192.0.2.0/29",
		Type:      "ipv4",
		Gateway:   "192.0.2.1",
	}); err != nil {
		t.Fatalf("add pool: %v", err)
	}

	vm1 := newTestVM(t, ctx, user.ID)
	vm2 := newTestVM(t, ctx, user.ID)
	vm3 := newTestVM(t, ctx, user.ID)

	alloc1, err := svc.Allocate(ctx, net.ID, vm1.ID, "02:00:00:00:00:01")
	if err != nil {
		t.Fatalf("allocate 1: %v", err)
	}
	alloc2, err := svc.Allocate(ctx, net.ID, vm2.ID, "02:00:00:00:00:02")
	if err != nil {
		t.Fatalf("allocate 2: %v", err)
	}
	if alloc1.IPAddress == alloc2.IPAddress {
		t.Error("duplicate IP allocated")
	}
	// Gateway (192.0.2.1) and network address must be skipped.
	for _, a := range []*models.IPAllocation{alloc1, alloc2} {
		if a.IPAddress == "192.0.2.1" || a.IPAddress == "192.0.2.0" {
			t.Errorf("reserved address allocated: %s", a.IPAddress)
		}
	}

	// Release then re-allocate must succeed.
	if err := svc.Release(ctx, vm1.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	alloc3, err := svc.Allocate(ctx, net.ID, vm3.ID, "02:00:00:00:00:03")
	if err != nil {
		t.Fatalf("reallocate: %v", err)
	}
	if alloc3.IPAddress != alloc1.IPAddress {
		t.Errorf("expected reallocation of %s, got %s", alloc1.IPAddress, alloc3.IPAddress)
	}
}

// TestIPAMConcurrentAllocation verifies no race: concurrent allocations never
// produce the same address.
func TestIPAMConcurrentAllocation(t *testing.T) {
	ctx := newContext(t)
	svc := networks.NewService(repos.Networks, repos.IPPools, nil)
	userSvc := newUserService(t)

	user, err := userSvc.Register(ctx, users.RegistrationRequest{
		Email: uniqueEmail("ipamrace"), Password: "password123",
		FirstName: "a", LastName: "b", LegalName: "a b",
		Country: "JP", PostalCode: "1", State: "x", City: "y", AddressLine1: "z",
	})
	if err != nil {
		t.Fatalf("register user: %v", err)
	}

	net, err := svc.CreateNetwork(ctx, &models.Network{
		Name:   "ipam-race-" + randSuffix(),
		Bridge: "br-race",
	})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	if _, err := svc.AddPool(ctx, &models.IPPool{
		NetworkID: net.ID,
		CIDR:      "198.51.100.0/28",
		Type:      "ipv4",
		Gateway:   "198.51.100.1",
	}); err != nil {
		t.Fatalf("add pool: %v", err)
	}

	const workers = 8
	mu := sync.Mutex{}
	seen := map[string]string{} // ip -> vm
	var wg sync.WaitGroup
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			vm := newTestVM(t, ctx, user.ID)
			alloc, err := svc.Allocate(ctx, net.ID, vm.ID, fmt.Sprintf("02:00:00:00:00:%02d", i+1))
			if err != nil {
				errs <- err
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if owner, exists := seen[alloc.IPAddress]; exists {
				errs <- fmt.Errorf("ip %s allocated to both %s and %s", alloc.IPAddress, owner, vm.ID)
				return
			}
			seen[alloc.IPAddress] = vm.ID
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if len(seen) != workers {
		t.Errorf("expected %d unique allocations, got %d", workers, len(seen))
	}
}

func randSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
