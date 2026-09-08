package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/tuna2134/vps/internal/controlplane/models"
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
}

func NewAuthHandlers(userSvc *users.Service) *AuthHandlers {
	return &AuthHandlers{users: userSvc}
}

func (h *AuthHandlers) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	u, err := h.users.Register(r.Context(), users.RegistrationRequest{
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
			writeError(w, http.StatusConflict, "EMAIL_TAKEN", "email already registered")
		default:
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		}
		return
	}
	writeData(w, http.StatusCreated, registerResponse{ID: u.ID, Email: u.Email})
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token  string `json:"token"`
	Expiry string `json:"expires_at"`
}

func (h *AuthHandlers) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	res, err := h.users.Login(r.Context(), req.Email, req.Password, remoteIP(r), r.UserAgent())
	if err != nil {
		switch {
		case errors.Is(err, users.ErrTooManyAttempts):
			writeError(w, http.StatusTooManyRequests, "TOO_MANY_ATTEMPTS", "too many failed login attempts")
		case errors.Is(err, users.ErrInvalidCredentials):
			writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid email or password")
		case errors.Is(err, users.ErrAccountDisabled):
			writeError(w, http.StatusForbidden, "ACCOUNT_DISABLED", "account is disabled")
		default:
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "an unexpected error occurred")
		}
		return
	}
	writeData(w, http.StatusOK, loginResponse{
		Token:  res.Token,
		Expiry: res.Session.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

func (h *AuthHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "missing bearer token")
		return
	}
	if err := h.users.Logout(r.Context(), token); err != nil {
		mapError(w, err)
		return
	}
	writeEmpty(w)
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

func (h *AuthHandlers) ListSessions(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	sessions, err := h.users.ListSessions(r.Context(), u.ID)
	if err != nil {
		mapError(w, err)
		return
	}
	out := make([]sessionResponse, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, toSessionResponse(s))
	}
	writeData(w, http.StatusOK, out)
}

func (h *AuthHandlers) RevokeSession(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	sessionID := strings.TrimPrefix(r.URL.Path, "/v1/auth/sessions/")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "missing session id")
		return
	}
	if err := h.users.RevokeSession(r.Context(), u.ID, sessionID); err != nil {
		mapError(w, err)
		return
	}
	writeEmpty(w)
}

func (h *AuthHandlers) RevokeAllSessions(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	sess := SessionFrom(r.Context())
	if err := h.users.RevokeAllSessions(r.Context(), u.ID, sess.ID); err != nil {
		mapError(w, err)
		return
	}
	writeEmpty(w)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (h *AuthHandlers) ChangePassword(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	sess := SessionFrom(r.Context())
	var req changePasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if err := h.users.ChangePassword(r.Context(), u.ID, req.CurrentPassword, req.NewPassword); err != nil {
		if errors.Is(err, users.ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "current password is incorrect")
			return
		}
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	// Invalidate all other sessions on password change.
	_ = h.users.RevokeAllSessions(r.Context(), u.ID, sess.ID)
	writeEmpty(w)
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

func (h *AuthHandlers) Me(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	roles := RolesFrom(r.Context())
	roleNames := make([]string, 0, len(roles))
	for _, role := range roles {
		roleNames = append(roleNames, string(role))
	}
	writeData(w, http.StatusOK, userResponse{
		ID:        u.ID,
		Email:     u.Email,
		FirstName: u.FirstName,
		LastName:  u.LastName,
		LegalName: u.LegalName,
		Status:    string(u.Status),
		Roles:     roleNames,
	})
}
