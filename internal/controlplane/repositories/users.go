package repositories

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tuna2134/vps/internal/controlplane/models"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrConflict   = errors.New("conflict")
	ErrForeignKey = errors.New("foreign key violation")
)

// UserRepository persists users. This repository intentionally excludes the
// mailing address (PII) which is owned by UserProfileRepository.
type UserRepository struct {
	db DBTX
}

func NewUserRepository(db DBTX) *UserRepository {
	return &UserRepository{db: db}
}

const userCols = `id, email, password_hash, first_name, last_name, legal_name, status, created_at, updated_at`

func scanUser(row pgx.Row) (*models.User, error) {
	var u models.User
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.FirstName, &u.LastName, &u.LegalName, &u.Status, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *UserRepository) Create(ctx context.Context, u *models.User) (*models.User, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, first_name, last_name, legal_name, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+userCols,
		strings.ToLower(u.Email), u.PasswordHash, u.FirstName, u.LastName, u.LegalName, u.Status)
	created, err := scanUser(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("create user: %w", ErrConflict)
		}
		return nil, fmt.Errorf("create user: %w", err)
	}
	return created, nil
}

func (r *UserRepository) GetByID(ctx context.Context, id string) (*models.User, error) {
	row := r.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id)
	u, err := scanUser(row)
	if isNoRowsOrInvalidUUID(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return u, nil
}

func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*models.User, error) {
	row := r.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE lower(email) = lower($1)`, email)
	u, err := scanUser(row)
	if isNoRowsOrInvalidUUID(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return u, nil
}

func (r *UserRepository) GetRoles(ctx context.Context, userID string) ([]models.Role, error) {
	rows, err := r.db.Query(ctx, `
		SELECT r.name FROM roles r
		JOIN user_roles ur ON ur.role_id = r.id
		WHERE ur.user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("get user roles: %w", err)
	}
	defer rows.Close()
	var roles []models.Role
	for rows.Next() {
		var role models.Role
		if err := rows.Scan(&role); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func (r *UserRepository) SetRoles(ctx context.Context, userID string, roles []models.Role) error {
	tx, ok := r.db.(pgx.Tx)
	if !ok {
		// The callers use a pool; run in a transaction when a tx is provided.
		return r.setRolesPool(ctx, userID, roles)
	}
	return r.setRolesTx(ctx, tx, userID, roles)
}

func (r *UserRepository) setRolesPool(ctx context.Context, userID string, roles []models.Role) error {
	return withTx(ctx, r.db, func(tx pgx.Tx) error {
		return r.setRolesTx(ctx, tx, userID, roles)
	})
}

func (r *UserRepository) setRolesTx(ctx context.Context, tx pgx.Tx, userID string, roles []models.Role) error {
	if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("clear roles: %w", err)
	}
	for _, role := range roles {
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_roles (user_id, role_id)
			SELECT $1, id FROM roles WHERE name = $2`, userID, string(role)); err != nil {
			return fmt.Errorf("assign role %s: %w", role, err)
		}
	}
	return nil
}

func (r *UserRepository) AssignRole(ctx context.Context, userID string, role models.Role) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id)
		SELECT $1, id FROM roles WHERE name = $2
		ON CONFLICT DO NOTHING`, userID, string(role))
	if err != nil {
		return fmt.Errorf("assign role: %w", err)
	}
	return nil
}

func (r *UserRepository) UpdateStatus(ctx context.Context, userID string, status models.UserStatus) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET status = $2, updated_at = now() WHERE id = $1`, userID, status)
	if err != nil {
		return fmt.Errorf("update user status: %w", err)
	}
	return nil
}

func (r *UserRepository) UpdatePasswordHash(ctx context.Context, userID, passwordHash string) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, userID, passwordHash)
	if err != nil {
		return fmt.Errorf("update password hash: %w", err)
	}
	return nil
}

func (r *UserRepository) UpdateProfile(ctx context.Context, userID string, firstName, lastName, legalName string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE users SET first_name = $2, last_name = $3, legal_name = $4, updated_at = now()
		WHERE id = $1`, userID, firstName, lastName, legalName)
	if err != nil {
		return fmt.Errorf("update user profile: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// isNoRowsOrInvalidUUID returns true when a query failed because the row does
// not exist or the id was not a valid UUID. Both should surface as NotFound.
func isNoRowsOrInvalidUUID(err error) bool {
	if isNoRowsOrInvalidUUID(err) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "22P02" { // invalid_text_representation
		return true
	}
	return false
}
