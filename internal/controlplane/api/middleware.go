package api

import (
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strings"
	"time"

	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/rbac"
	"github.com/tuna2134/vps/internal/controlplane/users"
)

// AuthMiddleware authenticates requests using the Bearer session token.
type AuthMiddleware struct {
	users *users.Service
	log   *slog.Logger
}

func NewAuthMiddleware(userSvc *users.Service, log *slog.Logger) *AuthMiddleware {
	return &AuthMiddleware{users: userSvc, log: log}
}

// Authenticate returns an http.Handler that resolves the session.
func (m *AuthMiddleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "missing bearer token")
			return
		}
		sess, u, err := m.users.Authenticate(r.Context(), token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid or expired session")
			return
		}
		roles, err := m.users.GetRoles(r.Context(), u.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "an unexpected error occurred")
			return
		}
		if ip, err := netip.ParseAddr(remoteIP(r)); err == nil {
			m.users.TouchSession(r.Context(), sess, ip)
		}
		ctx := withPrincipal(r.Context(), u, sess, roles)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole authorizes the principal against a minimum role.
func RequireRole(required models.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			roles := RolesFrom(r.Context())
			if len(roles) == 0 {
				writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
				return
			}
			if !rbac.Require(roles, required) {
				writeError(w, http.StatusForbidden, "FORBIDDEN", "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r.WithContext(r.Context()))
		})
	}
}

// Recoverer converts panics into 500 responses.
func Recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic recovered", "path", r.URL.Path, "panic", rec, "stack", string(debug.Stack()))
					writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "an unexpected error occurred")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// RequestLogger emits structured access logs without PII.
func RequestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			log.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"remote_ip", remoteIP(r),
			)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
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

// remoteIP returns the client IP, preferring the X-Forwarded-For header when
// behind a trusted proxy. The port is always stripped.
func remoteIP(r *http.Request) string {
	raw := r.RemoteAddr
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			raw = xff[:i]
		} else {
			raw = xff
		}
	}
	raw = strings.TrimSpace(raw)
	if host, _, err := net.SplitHostPort(raw); err == nil {
		return host
	}
	return raw
}