package repositories

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tuna2134/vps/internal/controlplane/models"
)

// DBTX is the minimal query interface used by repositories. Both *pgxpool.Pool
// and pgx.Tx implement it, allowing repositories to run inside transactions.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var _ DBTX = (*pgxpool.Pool)(nil)
var _ DBTX = (pgx.Tx)(nil)

// withTx runs fn inside a transaction, rolling back on error.
func withTx(ctx context.Context, db DBTX, fn func(tx pgx.Tx) error) error {
	tx, err := beginTx(ctx, db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// beginTx starts a transaction when the underlying DBTX is a pool. When db is
// already a pgx.Tx it is returned as-is so repositories can be composed.
func beginTx(ctx context.Context, db DBTX) (pgx.Tx, error) {
	if tx, ok := db.(pgx.Tx); ok {
		return tx, nil
	}
	if pool, ok := db.(interface {
		Begin(ctx context.Context) (pgx.Tx, error)
	}); ok {
		return pool.Begin(ctx)
	}
	return nil, fmt.Errorf("DBTX does not support transactions")
}

// UserProfileRepository owns the mailing address / legal address PII. It is
// isolated so it can be audited and restricted independently of the rest of
// the user domain.
type UserProfileRepository struct {
	db DBTX
}

func NewUserProfileRepository(db DBTX) *UserProfileRepository {
	return &UserProfileRepository{db: db}
}

const userProfileCols = `user_id, country, postal_code, state, city, address_line1, address_line2, created_at, updated_at`

func scanUserProfile(row pgx.Row) (*models.UserProfile, error) {
	var p models.UserProfile
	err := row.Scan(&p.UserID, &p.Country, &p.PostalCode, &p.State, &p.City, &p.AddressLine1, &p.AddressLine2, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *UserProfileRepository) Upsert(ctx context.Context, p *models.UserProfile) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO user_profiles (user_id, country, postal_code, state, city, address_line1, address_line2)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id) DO UPDATE SET
			country = EXCLUDED.country,
			postal_code = EXCLUDED.postal_code,
			state = EXCLUDED.state,
			city = EXCLUDED.city,
			address_line1 = EXCLUDED.address_line1,
			address_line2 = EXCLUDED.address_line2,
			updated_at = now()`,
		p.UserID, p.Country, p.PostalCode, p.State, p.City, p.AddressLine1, p.AddressLine2)
	if err != nil {
		return fmt.Errorf("upsert user profile: %w", err)
	}
	return nil
}

func (r *UserProfileRepository) GetByUserID(ctx context.Context, userID string) (*models.UserProfile, error) {
	row := r.db.QueryRow(ctx, `SELECT `+userProfileCols+` FROM user_profiles WHERE user_id = $1`, userID)
	p, err := scanUserProfile(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user profile: %w", err)
	}
	return p, nil
}

func (r *UserProfileRepository) Delete(ctx context.Context, userID string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM user_profiles WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("delete user profile: %w", err)
	}
	return nil
}