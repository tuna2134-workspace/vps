// Package networks manages network definitions (including the configurable
// bridge name), IP pools, and IPAM allocation.
package networks

import (
	"context"
	"errors"
	"net/netip"

	"github.com/tuna2134/vps/internal/controlplane/audit"
	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/repositories"
)

var (
	ErrConflict   = errors.New("conflict")
	ErrNotFound   = errors.New("not found")
	ErrNoCapacity = errors.New("network has no free addresses")
)

type Service struct {
	networks *repositories.NetworkRepository
	pools    *repositories.IPPoolRepository
	audit    *audit.Service
}

func NewService(
	networks *repositories.NetworkRepository,
	pools *repositories.IPPoolRepository,
	auditSvc *audit.Service,
) *Service {
	return &Service{networks: networks, pools: pools, audit: auditSvc}
}

func (s *Service) CreateNetwork(ctx context.Context, n *models.Network) (*models.Network, error) {
	if err := validateCIDR(n.IPv4CIDR); err != nil {
		return nil, err
	}
	if err := validateCIDR(n.IPv6CIDR); err != nil {
		return nil, err
	}
	created, err := s.networks.Create(ctx, n)
	if err != nil {
		if errors.Is(err, repositories.ErrConflict) {
			return nil, ErrConflict
		}
		return nil, err
	}
	_ = s.audit.Record(ctx, audit.Event{
		Action:       "network.create",
		ResourceType: "network",
		ResourceID:   created.ID,
		Metadata:     map[string]any{"bridge": created.Bridge},
	})
	return created, nil
}

func (s *Service) ListNetworks(ctx context.Context, activeOnly bool) ([]models.Network, error) {
	return s.networks.List(ctx, activeOnly)
}

func (s *Service) GetNetwork(ctx context.Context, id string) (*models.Network, error) {
	return s.networks.GetByID(ctx, id)
}

func (s *Service) SetNetworkStatus(ctx context.Context, id, status string) error {
	if err := s.networks.SetStatus(ctx, id, status); err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	_ = s.audit.Record(ctx, audit.Event{
		Action:       "network.set_status",
		ResourceType: "network",
		ResourceID:   id,
		Metadata:     map[string]any{"status": status},
	})
	return nil
}

// AddPool registers a new IP pool under a network.
func (s *Service) AddPool(ctx context.Context, pool *models.IPPool) (*models.IPPool, error) {
	if err := validateCIDR(pool.CIDR); err != nil {
		return nil, err
	}
	created, err := s.pools.CreatePool(ctx, pool)
	if err != nil {
		if errors.Is(err, repositories.ErrConflict) {
			return nil, ErrConflict
		}
		return nil, err
	}
	_ = s.audit.Record(ctx, audit.Event{
		Action:       "ip_pool.create",
		ResourceType: "network",
		ResourceID:   pool.NetworkID,
		Metadata:     map[string]any{"cidr": pool.CIDR},
	})
	return created, nil
}

func (s *Service) ListPools(ctx context.Context, networkID string) ([]models.IPPool, error) {
	return s.pools.ListPoolsByNetwork(ctx, networkID)
}

// Allocate assigns the next free address for a VM within the given network.
func (s *Service) Allocate(ctx context.Context, networkID, vmID, macAddress string) (*models.IPAllocation, error) {
	if _, err := s.networks.GetByID(ctx, networkID); err != nil {
		return nil, err
	}
	// Prefer the IPv4 pool; fall back to the IPv6 pool if none.
	pools, err := s.pools.ListPoolsByNetwork(ctx, networkID)
	if err != nil {
		return nil, err
	}
	if len(pools) == 0 {
		return nil, ErrNoCapacity
	}
	var pool *models.IPPool
	for i := range pools {
		if pools[i].Type == "ipv4" {
			pool = &pools[i]
			break
		}
	}
	if pool == nil {
		pool = &pools[0]
	}
	alloc, err := s.pools.Allocate(ctx, pool.ID, vmID, macAddress, pool.Gateway, 0)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return alloc, nil
}

// Release frees all allocations held by a VM.
func (s *Service) Release(ctx context.Context, vmID string) error {
	_ = s.audit.Record(ctx, audit.Event{
		Action:       "ip.release",
		ResourceType: "vm",
		ResourceID:   vmID,
	})
	return s.pools.Release(ctx, vmID)
}

func (s *Service) AllocationsByVM(ctx context.Context, vmID string) ([]models.IPAllocation, error) {
	return s.pools.GetByVM(ctx, vmID)
}

func validateCIDR(cidr string) error {
	if cidr == "" {
		return nil
	}
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return errors.New("invalid cidr: " + cidr)
	}
	if !p.Addr().IsValid() {
		return errors.New("invalid cidr: " + cidr)
	}
	return nil
}
