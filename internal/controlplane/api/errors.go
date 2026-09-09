package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/repositories"
	"github.com/tuna2134/vps/internal/controlplane/users"
	"github.com/tuna2134/vps/internal/controlplane/vms"
)

// principalKey is the gin context key for the authenticated principal.
const principalKey = "api.principal"

type principal struct {
	User    *models.User
	Session *models.Session
	Roles   []models.Role
}

// withPrincipal stores the authenticated user, session, and roles.
func withPrincipal(c *gin.Context, u *models.User, sess *models.Session, roles []models.Role) {
	c.Set(principalKey, &principal{User: u, Session: sess, Roles: roles})
}

// UserFrom returns the authenticated user.
func UserFrom(c *gin.Context) *models.User {
	if p, _ := c.Get(principalKey); p != nil {
		if pr, ok := p.(*principal); ok {
			return pr.User
		}
	}
	return nil
}

// SessionFrom returns the authenticated session.
func SessionFrom(c *gin.Context) *models.Session {
	if p, _ := c.Get(principalKey); p != nil {
		if pr, ok := p.(*principal); ok {
			return pr.Session
		}
	}
	return nil
}

// RolesFrom returns the principal's roles.
func RolesFrom(c *gin.Context) []models.Role {
	if p, _ := c.Get(principalKey); p != nil {
		if pr, ok := p.(*principal); ok {
			return pr.Roles
		}
	}
	return nil
}

// mapError converts a domain error into an HTTP error response.
func mapError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, users.ErrInvalidCredentials):
		writeError(c, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid email or password")
	case errors.Is(err, users.ErrEmailTaken):
		writeError(c, http.StatusConflict, "EMAIL_TAKEN", "email already registered")
	case errors.Is(err, users.ErrAccountDisabled):
		writeError(c, http.StatusForbidden, "ACCOUNT_DISABLED", "account is disabled")
	case errors.Is(err, users.ErrTooManyAttempts):
		writeError(c, http.StatusTooManyRequests, "TOO_MANY_ATTEMPTS", "too many failed login attempts")
	case errors.Is(err, users.ErrInvalidToken):
		writeError(c, http.StatusUnauthorized, "INVALID_SESSION", "invalid session token")
	case errors.Is(err, users.ErrSessionExpired):
		writeError(c, http.StatusUnauthorized, "SESSION_EXPIRED", "session expired")
	case errors.Is(err, users.ErrSessionRevoked):
		writeError(c, http.StatusUnauthorized, "SESSION_REVOKED", "session revoked")
	case errors.Is(err, repositories.ErrNotFound):
		writeError(c, http.StatusNotFound, "RESOURCE_NOT_FOUND", "resource not found")
	case errors.Is(err, vms.ErrNotFound):
		writeError(c, http.StatusNotFound, "RESOURCE_NOT_FOUND", "resource not found")
	case errors.Is(err, repositories.ErrConflict):
		writeError(c, http.StatusConflict, "CONFLICT", "resource already exists")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(c, http.StatusGatewayTimeout, "TIMEOUT", "operation timed out")
	default:
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "an unexpected error occurred")
	}
}

// mapServiceError handles service-level sentinel errors used across services.
func mapServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repositories.ErrNotFound):
		writeError(c, http.StatusNotFound, "RESOURCE_NOT_FOUND", "resource not found")
	case errors.Is(err, repositories.ErrConflict):
		writeError(c, http.StatusConflict, "CONFLICT", "resource already exists")
	default:
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "an unexpected error occurred")
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
