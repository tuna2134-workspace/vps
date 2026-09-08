package repositories

import (
	"context"
	"fmt"
	"time"
)

type LoginAttemptRepository struct {
	db DBTX
}

func NewLoginAttemptRepository(db DBTX) *LoginAttemptRepository {
	return &LoginAttemptRepository{db: db}
}

func (r *LoginAttemptRepository) Record(ctx context.Context, email, ip string, success bool) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO login_attempts (email, ip, success) VALUES (lower($1), $2, $3)`,
		email, ip, success)
	if err != nil {
		return fmt.Errorf("record login attempt: %w", err)
	}
	return nil
}

// CountRecentFailures returns the number of failed attempts for an email
// within the given window.
func (r *LoginAttemptRepository) CountRecentFailures(ctx context.Context, email string, window time.Duration) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `
		SELECT count(*) FROM login_attempts
		WHERE lower(email) = lower($1) AND success = false AND attempted_at > now() - $2::interval`,
		email, window.String()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count recent login failures: %w", err)
	}
	return n, nil
}

// CountRecentFailuresByIP counts failures from an IP (email-agnostic
// brute-force protection).
func (r *LoginAttemptRepository) CountRecentFailuresByIP(ctx context.Context, ip string, window time.Duration) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `
		SELECT count(*) FROM login_attempts
		WHERE ip = $1 AND success = false AND attempted_at > now() - $2::interval`,
		ip, window.String()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count recent login failures by ip: %w", err)
	}
	return n, nil
}

// CleanupOld deletes attempts older than the retention window.
func (r *LoginAttemptRepository) CleanupOld(ctx context.Context, retention time.Duration) error {
	_, err := r.db.Exec(ctx, `
		DELETE FROM login_attempts WHERE attempted_at < now() - $1::interval`, retention.String())
	if err != nil {
		return fmt.Errorf("cleanup login attempts: %w", err)
	}
	return nil
}

// LoginRateLimit summarizes the rate-limiting decision for a login.
type LoginRateLimit struct {
	Locked bool
	Delay  time.Duration
}