package integration

import (
	"sync"
	"testing"

	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/networks"
)

// TestIPv6PrefixDelegation verifies /48 -> /64 prefix allocation is sequential
// and never overlaps.
func TestIPv6PrefixDelegation(t *testing.T) {
	ctx := newContext(t)
	svc := networks.NewService(repos.Networks, repos.IPPools, nil)
	user := newTestUser(t, ctx)

	net, err := svc.CreateNetwork(ctx, &models.Network{
		Name: "v6-net-" + randSuffix(), Bridge: "br-v6",
	})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	pool, err := svc.AddPool(ctx, &models.IPPool{
		NetworkID:              net.ID,
		CIDR:                   "2001:db8:100::/48",
		Type:                   "ipv6",
		AllocationType:         "prefix",
		DelegationPrefixLength: 64,
	})
	if err != nil {
		t.Fatalf("add pool: %v", err)
	}

	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		vm := newTestVM(t, ctx, user.ID)
		alloc, err := svc.Allocate(ctx, net.ID, vm.ID, uniqueMAC())
		if err != nil {
			t.Fatalf("allocate %d: %v", i, err)
		}
		if alloc.Prefix != 64 {
			t.Fatalf("alloc %d: prefix = %d, want 64", i, alloc.Prefix)
		}
		if seen[alloc.IPAddress] {
			t.Fatalf("alloc %d: duplicate prefix %s", i, alloc.IPAddress)
		}
		seen[alloc.IPAddress] = true
	}

	want := []string{
		"2001:db8:100::/64",
		"2001:db8:100:1::/64",
		"2001:db8:100:2::/64",
		"2001:db8:100:3::/64",
		"2001:db8:100:4::/64",
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("missing expected prefix %s", w)
		}
	}
	_ = pool
}

// TestIPv6PrefixConcurrent verifies concurrent prefix allocations never
// overlap.
func TestIPv6PrefixConcurrent(t *testing.T) {
	ctx := newContext(t)
	svc := networks.NewService(repos.Networks, repos.IPPools, nil)
	user := newTestUser(t, ctx)

	net, err := svc.CreateNetwork(ctx, &models.Network{
		Name: "v6-conc-" + randSuffix(), Bridge: "br-v6c",
	})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	if _, err := svc.AddPool(ctx, &models.IPPool{
		NetworkID:              net.ID,
		CIDR:                   "2001:db8:200::/48",
		Type:                   "ipv6",
		AllocationType:         "prefix",
		DelegationPrefixLength: 64,
	}); err != nil {
		t.Fatalf("add pool: %v", err)
	}

	const n = 16
	allocated := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			vm := newTestVM(t, ctx, user.ID)
			alloc, err := svc.Allocate(ctx, net.ID, vm.ID, uniqueMAC())
			if err != nil {
				t.Errorf("allocate %d: %v", i, err)
				return
			}
			allocated[i] = alloc.IPAddress
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for _, a := range allocated {
		if a == "" {
			continue
		}
		if seen[a] {
			t.Fatalf("duplicate prefix allocated: %s", a)
		}
		seen[a] = true
	}
}
