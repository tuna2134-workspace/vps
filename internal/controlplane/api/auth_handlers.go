package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/tuna2134/vps/internal/controlplane/models"
	"github.com/tuna2134/vps/internal/controlplane/proxytrust"
	"github.com/tuna2134/vps/internal/controlplane/users"
)

type registerRequest struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	LegalName    string `json:"legal_name"`
	Country      string `json:"country"`
	PostalCode   string `json:"postal_code"`
	State        string `json:"state"`
	City         string `json:"city"`
	AddressLine1 string `json:"address_line_1"`
	AddressLine2 string `json:"address_line_2"`
}

type registerResponse struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// AuthHandlers exposes authentication endpoints.
type AuthHandlers struct {
	users *users.Service
	trust *proxytrust.ProxyTrust
}

func NewAuthHandlers(userSvc *users.Service, trust *proxytrust.ProxyTrust) *AuthHandlers {
	return &AuthHandlers{users: userSvc, trust: trust}
}

func (h *AuthHandlers) Register(c *gin.Context) {
	var req registerRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	u, err := h.users.Register(c.Request.Context(), users.RegistrationRequest{
		Email:        req.Email,
		Password:     req.Password,
		FirstName:    req.FirstName,
		LastName:     req.LastName,
		LegalName:    req.LegalName,
		Country:      req.Country,
		PostalCode:   req.PostalCode,
		State:        req.State,
		City:         req.City,
		AddressLine1: req.AddressLine1,
		AddressLine2: req.AddressLine2,
	})
	if err != nil {
		switch {
		case errors.Is(err, users.ErrEmailTaken):
			writeError(c, http.StatusConflict, "EMAIL_TAKEN", "email already registered")
		default:
			writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		}
		return
	}
	writeData(c, http.StatusCreated, registerResponse{ID: u.ID, Email: u.Email})
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token  string `json:"token"`
	Expiry string `json:"expires_at"`
}

func (h *AuthHandlers) Login(c *gin.Context) {
	var req loginRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	res, err := h.users.Login(c.Request.Context(), req.Email, req.Password, clientIP(h.trust, c.Request).String(), c.Request.UserAgent())
	if err != nil {
		switch {
		case errors.Is(err, users.ErrTooManyAttempts):
			writeError(c, http.StatusTooManyRequests, "TOO_MANY_ATTEMPTS", "too many failed login attempts")
		case errors.Is(err, users.ErrInvalidCredentials):
			writeError(c, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid email or password")
		case errors.Is(err, users.ErrAccountDisabled):
			writeError(c, http.StatusForbidden, "ACCOUNT_DISABLED", "account is disabled")
		default:
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "an unexpected error occurred")
		}
		return
	}
	writeData(c, http.StatusOK, loginResponse{
		Token:  res.Token,
		Expiry: res.Session.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

func (h *AuthHandlers) Logout(c *gin.Context) {
	token := bearerToken(c.Request)
	if token == "" {
		writeError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "missing bearer token")
		return
	}
	if err := h.users.Logout(c.Request.Context(), token); err != nil {
		mapError(c, err)
		return
	}
	writeEmpty(c)
}

// sessionResponse is the public view of a session. The token hash is NEVER
// exposed to clients.
type sessionResponse struct {
	ID         string  `json:"id"`
	UserID     string  `json:"user_id"`
	CreatedAt  string  `json:"created_at"`
	ExpiresAt  string  `json:"expires_at"`
	LastSeenAt string  `json:"last_seen_at"`
	IP         string  `json:"ip"`
	UserAgent  string  `json:"user_agent"`
	RevokedAt  *string `json:"revoked_at,omitempty"`
}

func toSessionResponse(s models.Session) sessionResponse {
	var revoked *string
	if s.RevokedAt != nil {
		v := s.RevokedAt.UTC().Format("2006-01-02T15:04:05Z")
		revoked = &v
	}
	return sessionResponse{
		ID:         s.ID,
		UserID:     s.UserID,
		CreatedAt:  s.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		ExpiresAt:  s.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"),
		LastSeenAt: s.LastSeenAt.UTC().Format("2006-01-02T15:04:05Z"),
		IP:         s.IP,
		UserAgent:  s.UserAgent,
		RevokedAt:  revoked,
	}
}

func (h *AuthHandlers) ListSessions(c *gin.Context) {
	u := UserFrom(c)
	sessions, err := h.users.ListSessions(c.Request.Context(), u.ID)
	if err != nil {
		mapError(c, err)
		return
	}
	out := make([]sessionResponse, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, toSessionResponse(s))
	}
	writeData(c, http.StatusOK, out)
}

func (h *AuthHandlers) RevokeSession(c *gin.Context) {
	u := UserFrom(c)
	sessionID := c.Param("id")
	if sessionID == "" {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "missing session id")
		return
	}
	if err := h.users.RevokeSession(c.Request.Context(), u.ID, sessionID); err != nil {
		mapError(c, err)
		return
	}
	writeEmpty(c)
}

func (h *AuthHandlers) RevokeAllSessions(c *gin.Context) {
	u := UserFrom(c)
	sess := SessionFrom(c)
	if err := h.users.RevokeAllSessions(c.Request.Context(), u.ID, sess.ID); err != nil {
		mapError(c, err)
		return
	}
	writeEmpty(c)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (h *AuthHandlers) ChangePassword(c *gin.Context) {
	u := UserFrom(c)
	sess := SessionFrom(c)
	var req changePasswordRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if err := h.users.ChangePassword(c.Request.Context(), u.ID, req.CurrentPassword, req.NewPassword); err != nil {
		if errors.Is(err, users.ErrInvalidCredentials) {
			writeError(c, http.StatusUnauthorized, "INVALID_CREDENTIALS", "current password is incorrect")
			return
		}
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	// Invalidate all other sessions on password change.
	_ = h.users.RevokeAllSessions(c.Request.Context(), u.ID, sess.ID)
	writeEmpty(c)
}

type userResponse struct {
	ID        string   `json:"id"`
	Email     string   `json:"email"`
	FirstName string   `json:"first_name"`
	LastName  string   `json:"last_name"`
	LegalName string   `json:"legal_name"`
	Status    string   `json:"status"`
	Roles     []string `json:"roles"`
}

func (h *AuthHandlers) Me(c *gin.Context) {
	u := UserFrom(c)
	roles := RolesFrom(c)
	roleNames := make([]string, 0, len(roles))
	for _, role := range roles {
		roleNames = append(roleNames, string(role))
	}
	writeData(c, http.StatusOK, userResponse{
		ID:        u.ID,
		Email:     u.Email,
		FirstName: u.FirstName,
		LastName:  u.LastName,
		LegalName: u.LegalName,
		Status:    string(u.Status),
		Roles:     roleNames,
	})
}
