package repositories

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/tuna2134/vps/internal/controlplane/models"
)

type ConsoleTokenRepository struct {
	db DBTX
}

func NewConsoleTokenRepository(db DBTX) *ConsoleTokenRepository {
	return &ConsoleTokenRepository{db: db}
}

const consoleTokenCols = `id, vm_id, user_id, token_hash, console_type, node_endpoint, vm_name, expires_at, used_at, created_at`

func scanConsoleToken(row pgx.Row) (*models.ConsoleToken, error) {
	var t models.ConsoleToken
	if err := row.Scan(&t.ID, &t.VMID, &t.UserID, &t.TokenHash, &t.ConsoleType,
		&t.NodeEndpoint, &t.VMName, &t.ExpiresAt, &t.UsedAt, &t.CreatedAt); err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *ConsoleTokenRepository) Create(ctx context.Context, t *models.ConsoleToken) (*models.ConsoleToken, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO console_tokens (vm_id, user_id, token_hash, console_type, node_endpoint, vm_name, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+consoleTokenCols,
		t.VMID, t.UserID, t.TokenHash, t.ConsoleType,
		t.NodeEndpoint, t.VMName, t.ExpiresAt)
	created, err := scanConsoleToken(row)
	if err != nil {
		return nil, fmt.Errorf("create console token: %w", err)
	}
	return created, nil
}

// Consume atomically validates and marks a token as used. It enforces
// single-use semantics and expiry.
func (r *ConsoleTokenRepository) Consume(ctx context.Context, tokenHash string) (*models.ConsoleToken, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE console_tokens SET used_at = now()
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
		RETURNING `+consoleTokenCols,
		tokenHash)
	consumed, err := scanConsoleToken(row)
	if err != nil {
		if isNoRowsOrInvalidUUID(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("consume console token: %w", err)
	}
	return consumed, nil
}

func (r *ConsoleTokenRepository) RevokeByVM(ctx context.Context, vmID string) error {
	_, err := r.db.Exec(ctx, `UPDATE console_tokens SET used_at = now() WHERE vm_id = $1 AND used_at IS NULL`, vmID)
	if err != nil {
		return fmt.Errorf("revoke console tokens: %w", err)
	}
	return nil
}
