package repositories

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tuna2134/vps/internal/controlplane/models"
)

type SessionRepository struct {
	db DBTX
}

func NewSessionRepository(db DBTX) *SessionRepository {
	return &SessionRepository{db: db}
}

const sessionCols = `id, user_id, token_hash, created_at, expires_at, last_seen_at, ip, user_agent, revoked_at`

func scanSession(row pgx.Row) (*models.Session, error) {
	var s models.Session
	var ip string
	if err := row.Scan(&s.ID, &s.UserID, &s.TokenHash, &s.CreatedAt, &s.ExpiresAt, &s.LastSeenAt, &ip, &s.UserAgent, &s.RevokedAt); err != nil {
		return nil, err
	}
	s.IP = ip
	return &s, nil
}

func (r *SessionRepository) Create(ctx context.Context, s *models.Session) (*models.Session, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO sessions (user_id, token_hash, expires_at, ip, user_agent)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+sessionCols,
		s.UserID, s.TokenHash, s.ExpiresAt, s.IP, s.UserAgent)
	created, err := scanSession(row)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return created, nil
}

func (r *SessionRepository) GetByTokenHash(ctx context.Context, tokenHash string) (*models.Session, error) {
	row := r.db.QueryRow(ctx, `SELECT `+sessionCols+` FROM sessions WHERE token_hash = $1`, tokenHash)
	s, err := scanSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get session by token hash: %w", err)
	}
	return s, nil
}

func (r *SessionRepository) GetByID(ctx context.Context, id string) (*models.Session, error) {
	row := r.db.QueryRow(ctx, `SELECT `+sessionCols+` FROM sessions WHERE id = $1`, id)
	s, err := scanSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get session by id: %w", err)
	}
	return s, nil
}

func (r *SessionRepository) ListByUser(ctx context.Context, userID string, includeRevoked bool) ([]models.Session, error) {
	q := `SELECT ` + sessionCols + ` FROM sessions WHERE user_id = $1`
	if !includeRevoked {
		q += ` AND revoked_at IS NULL`
	}
	q += ` ORDER BY created_at DESC`
	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	var sessions []models.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, *s)
	}
	return sessions, rows.Err()
}

func (r *SessionRepository) Revoke(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *SessionRepository) RevokeByUser(ctx context.Context, userID string, exceptSessionID string) (int64, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE sessions SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL AND id <> $2`, userID, exceptSessionID)
	if err != nil {
		return 0, fmt.Errorf("revoke user sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (r *SessionRepository) RevokeAllByUser(ctx context.Context, userID string) (int64, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE sessions SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	if err != nil {
		return 0, fmt.Errorf("revoke all user sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (r *SessionRepository) Touch(ctx context.Context, id string, ip netip.Addr) error {
	_, err := r.db.Exec(ctx, `
		UPDATE sessions SET last_seen_at = now(), ip = $2 WHERE id = $1`, id, ip.String())
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

func (r *SessionRepository) DeleteExpired(ctx context.Context) (int64, error) {
	tag, err := r.db.Exec(ctx, `
		DELETE FROM sessions WHERE expires_at < now() OR (revoked_at IS NOT NULL AND revoked_at < now() - interval '24 hours')`)
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// touchInterval bounds how often last_seen_at is updated.
var touchInterval = 5 * time.Minute

// ShouldTouch reports whether the session's last_seen_at is stale enough to
// warrant an update.
func ShouldTouch(lastSeen time.Time) bool {
	return time.Since(lastSeen) > touchInterval
}