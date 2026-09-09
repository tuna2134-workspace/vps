package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/proxytrust"
	"github.com/tuna2134/vps/internal/controlplane/rbac"
	"github.com/tuna2134/vps/internal/controlplane/users"
)

// AuthMiddleware authenticates requests using the Bearer session token.
type AuthMiddleware struct {
	users *users.Service
	trust *proxytrust.ProxyTrust
	log   *slog.Logger
}

func NewAuthMiddleware(userSvc *users.Service, trust *proxytrust.ProxyTrust, log *slog.Logger) *AuthMiddleware {
	return &AuthMiddleware{users: userSvc, trust: trust, log: log}
}

// Authenticate resolves the session and stores the principal in context.
func (m *AuthMiddleware) Authenticate(c *gin.Context) {
	token := bearerToken(c.Request)
	if token == "" {
		writeError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "missing bearer token")
		c.Abort()
		return
	}
	sess, u, err := m.users.Authenticate(c.Request.Context(), token)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid or expired session")
		c.Abort()
		return
	}
	roles, err := m.users.GetRoles(c.Request.Context(), u.ID)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "an unexpected error occurred")
		c.Abort()
		return
	}
	if ip := clientIP(m.trust, c.Request); ip.IsValid() {
		m.users.TouchSession(c.Request.Context(), sess, ip)
	}
	withPrincipal(c, u, sess, roles)
	c.Next()
}

// RequireRole authorizes the principal against a minimum role.
func RequireRole(required models.Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		roles := RolesFrom(c)
		if len(roles) == 0 {
			writeError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			c.Abort()
			return
		}
		if !rbac.Require(roles, required) {
			writeError(c, http.StatusForbidden, "FORBIDDEN", "insufficient permissions")
			c.Abort()
			return
		}
		c.Next()
	}
}

// Recoverer converts panics into 500 responses.
func Recoverer(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic recovered", "path", c.Request.URL.Path, "panic", rec, "stack", string(debug.Stack()))
				writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "an unexpected error occurred")
				c.Abort()
			}
		}()
		c.Next()
	}
}

// RequestLogger emits structured access logs without PII.
func RequestLogger(log *slog.Logger, trust *proxytrust.ProxyTrust) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Info("http request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"remote_ip", clientIP(trust, c.Request).String(),
		)
	}
}

// Timeout cancels the request context after the deadline. Handlers propagate
// the deadline to the services; mapError turns context.DeadlineExceeded into a
// 504 TIMEOUT response.
func Timeout(d time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), d)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// bearerToken extracts the token from the Authorization header.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) > len(prefix) && h[:len(prefix)] == prefix {
		return h[len(prefix):]
	}
	return ""
}

// clientIP resolves the client address using the trusted-proxy configuration.
// When trust is nil (or the peer is not trusted) forwarded headers are
// ignored and the TCP peer address is returned.
func clientIP(trust *proxytrust.ProxyTrust, r *http.Request) netip.Addr {
	if trust != nil {
		return trust.ClientIP(r)
	}
	addr, _ := netip.ParseAddr(parseRemoteAddr(r.RemoteAddr))
	return addr
}

func parseRemoteAddr(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}
