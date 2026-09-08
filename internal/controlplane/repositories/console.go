package repositories

import (
	"context"
	"fmt"

	"github.com/tuna2134/vps/internal/controlplane/models"
)

type ConsoleTokenRepository struct {
	db DBTX
}

func NewConsoleTokenRepository(db DBTX) *ConsoleTokenRepository {
	return &ConsoleTokenRepository{db: db}
}

func (r *ConsoleTokenRepository) Create(ctx context.Context, t *models.ConsoleToken) (*models.ConsoleToken, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO console_tokens (vm_id, user_id, token_hash, console_type, host, port, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, vm_id, user_id, token_hash, console_type, host, port, expires_at, used_at, created_at`,
		t.VMID, t.UserID, t.TokenHash, t.ConsoleType, t.Host, t.Port, t.ExpiresAt)
	var out models.ConsoleToken
	if err := row.Scan(&out.ID, &out.VMID, &out.UserID, &out.TokenHash, &out.ConsoleType,
		&out.Host, &out.Port, &out.ExpiresAt, &out.UsedAt, &out.CreatedAt); err != nil {
		return nil, fmt.Errorf("create console token: %w", err)
	}
	return &out, nil
}

// Consume atomically validates and marks a token as used. It enforces
// single-use semantics and expiry.
func (r *ConsoleTokenRepository) Consume(ctx context.Context, tokenHash string) (*models.ConsoleToken, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE console_tokens SET used_at = now()
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
		RETURNING id, vm_id, user_id, token_hash, console_type, host, port, expires_at, used_at, created_at`,
		tokenHash)
	var out models.ConsoleToken
	if err := row.Scan(&out.ID, &out.VMID, &out.UserID, &out.TokenHash, &out.ConsoleType,
		&out.Host, &out.Port, &out.ExpiresAt, &out.UsedAt, &out.CreatedAt); err != nil {
		if isNoRowsOrInvalidUUID(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("consume console token: %w", err)
	}
	return &out, nil
}

func (r *ConsoleTokenRepository) RevokeByVM(ctx context.Context, vmID string) error {
	_, err := r.db.Exec(ctx, `UPDATE console_tokens SET used_at = now() WHERE vm_id = $1 AND used_at IS NULL`, vmID)
	if err != nil {
		return fmt.Errorf("revoke console tokens: %w", err)
	}
	return nil
}
