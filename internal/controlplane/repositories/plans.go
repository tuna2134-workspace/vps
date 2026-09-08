package repositories

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/tuna2134/vps/internal/controlplane/models"
)

type PlanRepository struct {
	db DBTX
}

func NewPlanRepository(db DBTX) *PlanRepository {
	return &PlanRepository{db: db}
}

const planCols = `id, name, description, vcpu, memory_mb, disk_gb, bandwidth_gb, network_speed_mbps,
	ipv4_count, ipv6_prefix, monthly_price_cents, currency, version, active, created_at, updated_at`

func scanPlan(row pgx.Row) (*models.Plan, error) {
	var p models.Plan
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.VCPU, &p.MemoryMB, &p.DiskGB, &p.BandwidthGB,
		&p.NetworkSpeedMbps, &p.IPv4Count, &p.IPv6Prefix, &p.MonthlyPriceCents, &p.Currency,
		&p.Version, &p.Active, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *PlanRepository) Create(ctx context.Context, p *models.Plan) (*models.Plan, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO plans (name, description, vcpu, memory_mb, disk_gb, bandwidth_gb,
			network_speed_mbps, ipv4_count, ipv6_prefix, monthly_price_cents, currency)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING `+planCols,
		p.Name, p.Description, p.VCPU, p.MemoryMB, p.DiskGB, p.BandwidthGB,
		p.NetworkSpeedMbps, p.IPv4Count, p.IPv6Prefix, p.MonthlyPriceCents, p.Currency)
	created, err := scanPlan(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("create plan: %w", ErrConflict)
		}
		return nil, fmt.Errorf("create plan: %w", err)
	}
	return created, nil
}

func (r *PlanRepository) GetByID(ctx context.Context, id string) (*models.Plan, error) {
	row := r.db.QueryRow(ctx, `SELECT `+planCols+` FROM plans WHERE id = $1`, id)
	p, err := scanPlan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get plan: %w", err)
	}
	return p, nil
}

func (r *PlanRepository) List(ctx context.Context, activeOnly bool) ([]models.Plan, error) {
	q := `SELECT ` + planCols + ` FROM plans`
	if activeOnly {
		q += ` WHERE active`
	}
	q += ` ORDER BY monthly_price_cents`
	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	defer rows.Close()
	var out []models.Plan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// Update records a new version snapshot and applies the new plan definition.
// Existing subscriptions are untouched (they reference the historical snapshot
// via plan_versions).
func (r *PlanRepository) Update(ctx context.Context, planID string, p *models.Plan) (*models.Plan, error) {
	tx, err := beginTx(ctx, r.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	// Snapshot current version into plan_versions.
	if _, err := tx.Exec(ctx, `
		INSERT INTO plan_versions (plan_id, version, name, vcpu, memory_mb, disk_gb, bandwidth_gb,
			network_speed_mbps, ipv4_count, ipv6_prefix, monthly_price_cents, currency)
		SELECT id, version, name, vcpu, memory_mb, disk_gb, bandwidth_gb,
			network_speed_mbps, ipv4_count, ipv6_prefix, monthly_price_cents, currency
		FROM plans WHERE id = $1`, planID); err != nil {
		return nil, fmt.Errorf("snapshot plan version: %w", err)
	}

	row := tx.QueryRow(ctx, `
		UPDATE plans SET
			name = $2, description = $3, vcpu = $4, memory_mb = $5, disk_gb = $6,
			bandwidth_gb = $7, network_speed_mbps = $8, ipv4_count = $9, ipv6_prefix = $10,
			monthly_price_cents = $11, currency = $12, version = version + 1, updated_at = now()
		WHERE id = $1
		RETURNING `+planCols,
		planID, p.Name, p.Description, p.VCPU, p.MemoryMB, p.DiskGB, p.BandwidthGB,
		p.NetworkSpeedMbps, p.IPv4Count, p.IPv6Prefix, p.MonthlyPriceCents, p.Currency)
	updated, err := scanPlan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update plan: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit plan update: %w", err)
	}
	return updated, nil
}

func (r *PlanRepository) SetActive(ctx context.Context, planID string, active bool) error {
	tag, err := r.db.Exec(ctx, `UPDATE plans SET active = $2, updated_at = now() WHERE id = $1`, planID, active)
	if err != nil {
		return fmt.Errorf("set plan active: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PlanRepository) ListVersions(ctx context.Context, planID string) ([]models.Plan, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, plan_id, version, name, vcpu, memory_mb, disk_gb, bandwidth_gb,
			network_speed_mbps, ipv4_count, ipv6_prefix, monthly_price_cents, currency
		FROM plan_versions WHERE plan_id = $1 ORDER BY version DESC`, planID)
	if err != nil {
		return nil, fmt.Errorf("list plan versions: %w", err)
	}
	defer rows.Close()
	var out []models.Plan
	for rows.Next() {
		var p models.Plan
		if err := rows.Scan(&p.ID, &p.PlanID, &p.Version, &p.Name, &p.VCPU, &p.MemoryMB, &p.DiskGB,
			&p.BandwidthGB, &p.NetworkSpeedMbps, &p.IPv4Count, &p.IPv6Prefix, &p.MonthlyPriceCents, &p.Currency); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}