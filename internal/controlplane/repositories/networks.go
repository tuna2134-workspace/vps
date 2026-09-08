package repositories

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/jackc/pgx/v5"
	"github.com/tuna2134/vps/internal/controlplane/models"
)

type NetworkRepository struct {
	db DBTX
}

func NewNetworkRepository(db DBTX) *NetworkRepository {
	return &NetworkRepository{db: db}
}

const networkCols = `id, name, description, bridge, ipv4_cidr, ipv6_cidr, dns1, dns2, status, created_at, updated_at`

func scanNetwork(row pgx.Row) (*models.Network, error) {
	var n models.Network
	err := row.Scan(&n.ID, &n.Name, &n.Description, &n.Bridge, &n.IPv4CIDR, &n.IPv6CIDR,
		&n.DNS1, &n.DNS2, &n.Status, &n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func (r *NetworkRepository) Create(ctx context.Context, n *models.Network) (*models.Network, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO networks (name, description, bridge, ipv4_cidr, ipv6_cidr, dns1, dns2)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+networkCols,
		n.Name, n.Description, n.Bridge, n.IPv4CIDR, n.IPv6CIDR, n.DNS1, n.DNS2)
	created, err := scanNetwork(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("create network: %w", ErrConflict)
		}
		return nil, fmt.Errorf("create network: %w", err)
	}
	return created, nil
}

func (r *NetworkRepository) GetByID(ctx context.Context, id string) (*models.Network, error) {
	row := r.db.QueryRow(ctx, `SELECT `+networkCols+` FROM networks WHERE id = $1`, id)
	n, err := scanNetwork(row)
	if isNoRowsOrInvalidUUID(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get network: %w", err)
	}
	return n, nil
}

func (r *NetworkRepository) List(ctx context.Context, activeOnly bool) ([]models.Network, error) {
	q := `SELECT ` + networkCols + ` FROM networks`
	if activeOnly {
		q += ` WHERE status = 'active'`
	}
	q += ` ORDER BY name`
	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list networks: %w", err)
	}
	defer rows.Close()
	var out []models.Network
	for rows.Next() {
		n, err := scanNetwork(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

func (r *NetworkRepository) SetStatus(ctx context.Context, id, status string) error {
	tag, err := r.db.Exec(ctx, `UPDATE networks SET status = $2, updated_at = now() WHERE id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("set network status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// IPPoolRepository manages IP pools and allocations. Allocation is
// transactional so concurrent provisioning requests can never receive the
// same address.
type IPPoolRepository struct {
	db DBTX
}

func NewIPPoolRepository(db DBTX) *IPPoolRepository {
	return &IPPoolRepository{db: db}
}

func (r *IPPoolRepository) CreatePool(ctx context.Context, pool *models.IPPool) (*models.IPPool, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO ip_pools (network_id, cidr, type, gateway)
		VALUES ($1, $2, $3, $4)
		RETURNING id, network_id, cidr, type, gateway`,
		pool.NetworkID, pool.CIDR, pool.Type, pool.Gateway)
	var out models.IPPool
	if err := row.Scan(&out.ID, &out.NetworkID, &out.CIDR, &out.Type, &out.Gateway); err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("create ip pool: %w", ErrConflict)
		}
		return nil, fmt.Errorf("create ip pool: %w", err)
	}
	return &out, nil
}

func (r *IPPoolRepository) ListPoolsByNetwork(ctx context.Context, networkID string) ([]models.IPPool, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, network_id, cidr, type, gateway FROM ip_pools WHERE network_id = $1`, networkID)
	if err != nil {
		return nil, fmt.Errorf("list ip pools: %w", err)
	}
	defer rows.Close()
	var out []models.IPPool
	for rows.Next() {
		var p models.IPPool
		if err := rows.Scan(&p.ID, &p.NetworkID, &p.CIDR, &p.Type, &p.Gateway); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *IPPoolRepository) GetPoolByID(ctx context.Context, poolID string) (*models.IPPool, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, network_id, cidr, type, gateway FROM ip_pools WHERE id = $1`, poolID)
	var p models.IPPool
	if err := row.Scan(&p.ID, &p.NetworkID, &p.CIDR, &p.Type, &p.Gateway); err != nil {
		if isNoRowsOrInvalidUUID(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get ip pool: %w", err)
	}
	return &p, nil
}

// Allocate allocates the next free address in a pool for a VM. It runs inside
// a transaction and uses a row lock on the pool to serialize concurrent
// allocations, then scans for the first unallocated address.
func (r *IPPoolRepository) Allocate(ctx context.Context, poolID, vmID, macAddress, gateway string, prefix int) (*models.IPAllocation, error) {
	tx, err := beginTx(ctx, r.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var pool models.IPPool
	if err := tx.QueryRow(ctx, `
		SELECT id, network_id, cidr, type, gateway FROM ip_pools WHERE id = $1 FOR UPDATE`, poolID).
		Scan(&pool.ID, &pool.NetworkID, &pool.CIDR, &pool.Type, &pool.Gateway); err != nil {
		if isNoRowsOrInvalidUUID(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lock ip pool: %w", err)
	}
	if gateway == "" {
		gateway = pool.Gateway
	}
	if prefix == 0 {
		p, err := netip.ParsePrefix(pool.CIDR)
		if err != nil {
			return nil, fmt.Errorf("parse pool cidr: %w", err)
		}
		prefix = p.Bits()
	}

	addr, err := r.nextFreeAddress(ctx, tx, poolID, pool.CIDR, gateway)
	if err != nil {
		return nil, err
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO ip_allocations (pool_id, vm_id, ip_address, mac_address, gateway, prefix, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'allocated')
		RETURNING id, pool_id, vm_id, ip_address, mac_address, gateway, prefix, status, allocated_at, released_at`,
		poolID, vmID, addr, macAddress, gateway, prefix)
	var a models.IPAllocation
	if err := row.Scan(&a.ID, &a.PoolID, &a.VMID, &a.IPAddress, &a.MACAddress, &a.Gateway,
		&a.Prefix, &a.Status, &a.AllocatedAt, &a.ReleasedAt); err != nil {
		return nil, fmt.Errorf("insert ip allocation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit ip allocation: %w", err)
	}
	return &a, nil
}

func (r *IPPoolRepository) nextFreeAddress(ctx context.Context, tx pgx.Tx, poolID, cidr, gateway string) (string, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return "", fmt.Errorf("parse pool cidr: %w", err)
	}
	gw := netip.Addr{}
	if gateway != "" {
		if a, err := netip.ParseAddr(gateway); err == nil {
			gw = a
		}
	}
	// Scan every host address in the prefix (bounded for safety).
	total := prefix.Addr().Next()
	last := lastUsableAddress(prefix)
	cap := int64(1) << 24 // safety bound
	if prefix.Addr().Is4() {
		cap = 1 << (32 - prefix.Bits())
	}
	for i := int64(0); i < cap; i++ {
		if total.IsValid() && total == last {
			break
		}
		if (gw.IsValid() && total == gw) || isBroadcast(prefix, total) {
			total = total.Next()
			continue
		}
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM ip_allocations
				WHERE pool_id = $1 AND ip_address = $2 AND status = 'allocated'
			)`, poolID, total.String()).Scan(&exists); err != nil {
			return "", fmt.Errorf("check ip availability: %w", err)
		}
		if !exists {
			return total.String(), nil
		}
		total = total.Next()
	}
	return "", fmt.Errorf("ip pool %s exhausted", cidr)
}

// lastUsableAddress returns the last address within the prefix (broadcast for
// IPv4, the masked max for IPv6).
func lastUsableAddress(p netip.Prefix) netip.Addr {
	if p.Addr().Is4() {
		bytes := p.Masked().Addr().As4()
		n := 32 - p.Bits()
		for i := 3; i >= 0 && n > 0; i-- {
			if n >= 8 {
				bytes[i] = 0xFF
				n -= 8
			} else {
				bytes[i] |= byte(0xFF << (8 - n))
				n = 0
			}
		}
		return netip.AddrFrom4(bytes)
	}
	bytes := p.Masked().Addr().As16()
	n := 128 - p.Bits()
	for i := 15; i >= 0 && n > 0; i-- {
		if n >= 8 {
			bytes[i] = 0xFF
			n -= 8
		} else {
			bytes[i] |= byte(0xFF << (8 - n))
			n = 0
		}
	}
	return netip.AddrFrom16(bytes)
}

func isBroadcast(p netip.Prefix, addr netip.Addr) bool {
	if !addr.Is4() {
		return false
	}
	return addr == lastUsableAddress(p)
}

func usableAddresses(p netip.Prefix) int64 {
	if p.Addr().Is4() {
		bits := p.Bits()
		if bits >= 31 {
			return 0
		}
		return 1<<(32-bits) - 3 // minus network, gateway, broadcast
	}
	return 1 << (128 - p.Bits())
}

func (r *IPPoolRepository) Release(ctx context.Context, vmID string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE ip_allocations SET status = 'released', released_at = now()
		WHERE vm_id = $1 AND status = 'allocated'`, vmID)
	if err != nil {
		return fmt.Errorf("release ip allocations: %w", err)
	}
	_ = tag
	return nil
}

func (r *IPPoolRepository) GetByVM(ctx context.Context, vmID string) ([]models.IPAllocation, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, pool_id, vm_id, ip_address, mac_address, gateway, prefix, status, allocated_at, released_at
		FROM ip_allocations WHERE vm_id = $1 ORDER BY allocated_at`, vmID)
	if err != nil {
		return nil, fmt.Errorf("get allocations by vm: %w", err)
	}
	defer rows.Close()
	var out []models.IPAllocation
	for rows.Next() {
		var a models.IPAllocation
		if err := rows.Scan(&a.ID, &a.PoolID, &a.VMID, &a.IPAddress, &a.MACAddress, &a.Gateway,
			&a.Prefix, &a.Status, &a.AllocatedAt, &a.ReleasedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
