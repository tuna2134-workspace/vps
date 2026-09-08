package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/repositories"
	"github.com/tuna2134/vps/internal/controlplane/users"
)

// ctxKey is a private type for context keys.
type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxSession
	ctxRoles
)

// withPrincipal stores the authenticated user, session, and roles in context.
func withPrincipal(ctx context.Context, u *models.User, sess *models.Session, roles []models.Role) context.Context {
	ctx = context.WithValue(ctx, ctxUser, u)
	ctx = context.WithValue(ctx, ctxSession, sess)
	return context.WithValue(ctx, ctxRoles, roles)
}

// UserFrom returns the authenticated user from context.
func UserFrom(ctx context.Context) *models.User {
	u, _ := ctx.Value(ctxUser).(*models.User)
	return u
}

// SessionFrom returns the authenticated session from context.
func SessionFrom(ctx context.Context) *models.Session {
	s, _ := ctx.Value(ctxSession).(*models.Session)
	return s
}

// RolesFrom returns the principal's roles.
func RolesFrom(ctx context.Context) []models.Role {
	roles, _ := ctx.Value(ctxRoles).([]models.Role)
	return roles
}

// mapError converts a domain error into an HTTP error response.
func mapError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, users.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid email or password")
	case errors.Is(err, users.ErrEmailTaken):
		writeError(w, http.StatusConflict, "EMAIL_TAKEN", "email already registered")
	case errors.Is(err, users.ErrAccountDisabled):
		writeError(w, http.StatusForbidden, "ACCOUNT_DISABLED", "account is disabled")
	case errors.Is(err, users.ErrTooManyAttempts):
		writeError(w, http.StatusTooManyRequests, "TOO_MANY_ATTEMPTS", "too many failed login attempts")
	case errors.Is(err, users.ErrInvalidToken):
		writeError(w, http.StatusUnauthorized, "INVALID_SESSION", "invalid session token")
	case errors.Is(err, users.ErrSessionExpired):
		writeError(w, http.StatusUnauthorized, "SESSION_EXPIRED", "session expired")
	case errors.Is(err, users.ErrSessionRevoked):
		writeError(w, http.StatusUnauthorized, "SESSION_REVOKED", "session revoked")
	case errors.Is(err, repositories.ErrNotFound):
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "resource not found")
	case errors.Is(err, repositories.ErrConflict):
		writeError(w, http.StatusConflict, "CONFLICT", "resource already exists")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "TIMEOUT", "operation timed out")
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "an unexpected error occurred")
	}
}

// mapServiceError handles service-level sentinel errors used across services.
func mapServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repositories.ErrNotFound):
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "resource not found")
	case errors.Is(err, repositories.ErrConflict):
		writeError(w, http.StatusConflict, "CONFLICT", "resource already exists")
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "an unexpected error occurred")
	}
}

// errorCodeForRPC extracts the user-facing error code from a message.
func errorCodeForRPC(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "agent unavailable"):
		return "AGENT_UNAVAILABLE"
	case strings.Contains(msg, "no capacity"):
		return "NO_CAPACITY"
	}
	return fmt.Sprintf("AGENT_ERROR: %s", msg)
}
